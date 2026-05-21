// Package config parses rrsh's allowlist configuration (JSON).
//
// Schema:
//
//	{
//	  "name":         "ntfy-prod",         // optional, MCP serverInfo.name
//	  "instructions": "host-specific...",   // optional, MCP initialize.instructions
//	  "sudo":         false,                // master switch for elevation
//	  "commands": [
//	    { "command": ["/usr/bin/whoami"] },
//	    { "command": ["/usr/bin/ls", "-la", "/var/log/.*"] },
//	    { "command": ["/usr/bin/ping", "-c", "\\d+", ".+"], "timeout": "30s" },
//	    { "command": ["/bin/systemctl", "restart", "ntfy"], "as": ["root"] },
//	    { "command": ["/usr/bin/journalctl", "-u", "ntfy"], "as": ["self", "root"] }
//	  ]
//	}
//
// Fields on each command entry:
//
//   - command  list of regexes (required, length >= 1). Element 0 matches
//              the binary path; elements 1..N-1 match argv[0..N-2].
//              All patterns are auto-anchored (wrapped in ^(?:…)$).
//   - timeout  per-command timeout, e.g. "30s" (default: DefaultTimeout)
//   - as       list of users the command may run as (default: ["self"])
//   - description  free text shown to AI via list_commands
//
// A call matches a rule when:
//   - the AI's path matches command[0]'s regex; AND
//   - argv has exactly len(command)-1 elements; AND
//   - each argv[i] matches command[i+1]'s regex.
//
// Multiple rules with the same command[0] (e.g. literal "/usr/bin/ps")
// are allowed — the matcher tries each in order and takes the first whose
// shape matches. Use this to express alternative argv shapes (e.g.
// `ps aux` vs `ps -ef` vs `ps -eo <fmt>`).
//
// Treating command[0] as a regex means an operator can write e.g.
// "/usr/bin/(cat|head)" to share a rule across related binaries. The
// matcher still requires the AI's path to start with `/` as
// defense-in-depth against accidentally-permissive regexes.
//
// `self` in an `as` list resolves to the SSH user at runtime. Other entries are
// real usernames (e.g. "root", "deploy"). Unknown fields are rejected — the
// parser is strict because it sits on the privileged trust boundary.
//
// There is no top-level `timeout` — DefaultTimeout (30s) is the baseline for
// every command, and individual rules opt out by setting their own `timeout`.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"
)

// SelfUser is the magic token in `as` lists meaning "the SSH user who invoked
// rrsh". Resolved at runtime against $SUDO_USER (in the privileged
// subcommand) or the current user (in the unprivileged process).
const SelfUser = "self"

// CommandRule is one allowlist entry. The CommandPatterns slice is the
// rule's only structural specification: index 0 matches the binary path,
// and indices 1..N-1 match argv[0..N-2]. A call passes the rule iff its
// path matches CommandPatterns[0], argv length equals len(CommandPatterns)-1,
// and every argv[i] matches CommandPatterns[i+1].
type CommandRule struct {
	// CommandPatterns is the compiled, auto-anchored regex list. Always
	// has len >= 1 after Parse — the parser refuses empty commands.
	CommandPatterns []*regexp.Regexp
	// CommandSource preserves the operator-authored regex strings before
	// auto-anchoring, used by list_commands to show what was written.
	CommandSource []string
	Timeout       time.Duration
	// As lists the users the command may run as. SelfUser ("self") is the
	// SSH user. Other entries are real usernames (e.g. "root", "deploy").
	// Defaults to [SelfUser] when omitted in the config.
	As []string
	// Description is a free-text explanation of the rule shown to the AI
	// consumer via list_commands. Optional.
	Description string
}

// Config is the parsed on-disk allowlist. Top-level fields control
// server-wide behavior; per-rule fields live on CommandRule.
type Config struct {
	// Name overrides the default server name reported in MCP's
	// serverInfo.name (e.g. "ntfy-prod-1"). Optional. Defaults to "rrsh".
	Name string
	// Instructions is host-specific text returned in MCP's
	// initialize.instructions field. This is where you tell the AI what
	// this host is, what kind of access it has, and any caveats. The AI
	// fetches this on first contact, so it can replace the need for a
	// per-host system prompt. Optional.
	Instructions string
	// Sudo, when false (the default), disables every elevation path.
	// The `rrsh sudo` privileged subcommand refuses to run, and the
	// MCP server denies any run_command call whose resolved target
	// differs from the SSH user. This is the second line of defense
	// alongside the sudoers file: even if the .deb installs the
	// sudoers grant, elevation does nothing until the operator
	// explicitly sets "sudo": true in the config.
	Sudo     bool
	Commands []CommandRule
}

// rawConfig mirrors the on-disk JSON shape. We convert + validate after
// unmarshal so the in-memory Config can hold compiled regexes.
type rawConfig struct {
	Name         string    `json:"name"`
	Instructions string    `json:"instructions"`
	Sudo         bool      `json:"sudo"`
	Commands     []rawRule `json:"commands"`
}

type rawRule struct {
	Command     []string `json:"command"`
	Timeout     string   `json:"timeout"`
	As          []string `json:"as"`
	Description string   `json:"description"`
}

// Load reads and parses the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return Parse(data)
}

// Parse parses raw config bytes.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	cfg := &Config{
		Name:         raw.Name,
		Instructions: raw.Instructions,
		Sudo:         raw.Sudo,
	}
	for i, r := range raw.Commands {
		rule, err := convertRule(r)
		if err != nil {
			return nil, fmt.Errorf("commands[%d]: %w", i, err)
		}
		cfg.Commands = append(cfg.Commands, rule)
	}
	return cfg, nil
}

func convertRule(r rawRule) (CommandRule, error) {
	if len(r.Command) == 0 {
		return CommandRule{}, fmt.Errorf("`command` is required and must have at least one element (the path regex)")
	}
	// Compile each entry into an auto-anchored regex. Wrap in ^(?:…)$
	// so MatchString — which is unanchored — cannot silently allow a
	// substring; idempotent if the operator already wrote ^…$.
	patterns := make([]*regexp.Regexp, len(r.Command))
	for i, src := range r.Command {
		re, err := regexp.Compile("^(?:" + src + ")$")
		if err != nil {
			return CommandRule{}, fmt.Errorf("invalid `command[%d]` regex %q: %w", i, src, err)
		}
		patterns[i] = re
	}
	rule := CommandRule{
		CommandPatterns: patterns,
		CommandSource:   append([]string(nil), r.Command...),
	}
	if r.Timeout != "" {
		d, err := time.ParseDuration(r.Timeout)
		if err != nil {
			return CommandRule{}, fmt.Errorf("invalid `timeout` for %s: %w", r.Command[0], err)
		}
		rule.Timeout = d
	}
	as, err := normalizeAs(r.Command[0], r.As)
	if err != nil {
		return CommandRule{}, err
	}
	rule.As = as
	rule.Description = r.Description
	return rule, nil
}

// validUsername matches a conservative subset of POSIX login names:
// lowercase letter or underscore start, then alnum/underscore/dash, up
// to 32 chars, optionally ending in `$` (Samba machine account form).
// The rrsh-internal SelfUser token is allowed separately.
//
// The point is to refuse values that would survive into sudo's argv as
// stray flags (e.g. "-h", "--", "-u") or contain characters sudo or the
// shell could interpret. sudo itself does not shell-interpret usernames,
// so existing weird names would just fail to look up — but failing
// closed in the config is cheaper than tracing why a rule mysteriously
// stopped working in production.
var validUsername = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)

// normalizeAs validates an `as` list and defaults it to ["self"] when omitted.
// pathHint is the operator-written command[0] regex, used only for error
// messages so the operator can identify which rule failed validation.
func normalizeAs(pathHint string, as []string) ([]string, error) {
	if len(as) == 0 {
		return []string{SelfUser}, nil
	}
	seen := make(map[string]bool, len(as))
	out := make([]string, 0, len(as))
	for _, u := range as {
		if u == "" {
			return nil, fmt.Errorf("`as` for %s has an empty entry", pathHint)
		}
		if seen[u] {
			return nil, fmt.Errorf("`as` for %s has duplicate entry %q", pathHint, u)
		}
		if u != SelfUser && !validUsername.MatchString(u) {
			return nil, fmt.Errorf("`as` for %s has invalid username %q (expected POSIX login name)", pathHint, u)
		}
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}
