// Package config parses rrsh's allowlist configuration (JSON).
//
// Schema:
//
//	{
//	  "name":         "ntfy-prod",         // optional, MCP serverInfo.name
//	  "instructions": "host-specific...",   // optional, MCP initialize.instructions
//	  "sudo":         false,                // master switch for elevation
//	  "commands": [
//	    { "path": "/usr/bin/whoami" },
//	    { "path": "/usr/bin/ls",     "args": ["-la", "/var/log/.*"] },
//	    { "path": "/usr/bin/ping",   "args": ["-c", "\\d+", ".+"], "timeout": "30s" },
//	    { "path": "/bin/systemctl",  "args": ["restart", "ntfy"], "as": ["root"] },
//	    { "path": "/usr/bin/journalctl", "args": ["-u", "ntfy"], "as": ["self", "root"] }
//	  ]
//	}
//
// Fields on each command entry:
//
//   - path     absolute path to the binary (required)
//   - args     list of regexes, one per argv element (default: any args allowed)
//   - timeout  per-command timeout, e.g. "30s" (default: DefaultTimeout)
//   - as       list of users the command may run as (default: ["self"])
//
// The `args` field is a list of regular expressions, one per argv element.
// A call is allowed only if argv has exactly the same length AND every
// element matches its corresponding regex. Patterns are auto-anchored
// (wrapped in ^(?:…)$). Multiple rules with the same `path` are allowed —
// the matcher tries each in order and takes the first whose `args` shape
// matches. Use this to express alternative argv shapes (e.g. `ps aux`
// vs `ps -eo <fmt>`).
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
	"strings"
	"time"
)

// SelfUser is the magic token in `as` lists meaning "the SSH user who invoked
// rrsh". Resolved at runtime against $SUDO_USER (in the privileged
// subcommand) or the current user (in the unprivileged process).
const SelfUser = "self"

// CommandRule is one allowlist entry. It is matched against an incoming
// (path, argv) pair: Path must equal the absolute binary path, and — if
// ArgsPatterns is non-nil — argv must have exactly len(ArgsPatterns)
// elements with each element matching its corresponding regex.
type CommandRule struct {
	Path string
	// ArgsPatterns is a slice of per-argv-element regexes (each
	// auto-anchored). A nil slice means the rule accepts any argv shape;
	// a non-nil (possibly empty) slice means argv length must equal
	// len(ArgsPatterns) and ArgsPatterns[i].MatchString(argv[i]) for all i.
	ArgsPatterns []*regexp.Regexp
	// ArgsSource preserves the operator-authored patterns (before
	// auto-anchoring) so list_commands can show what was originally
	// written. nil when no `args` was specified.
	ArgsSource []string
	Timeout    time.Duration
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

// rawConfig mirrors the on-disk JSON shape. Strings for timeout/args keep the
// JSON readable; we convert + validate after unmarshal.
type rawConfig struct {
	Name         string    `json:"name"`
	Instructions string    `json:"instructions"`
	Sudo         bool      `json:"sudo"`
	Commands     []rawRule `json:"commands"`
}

type rawRule struct {
	Path string `json:"path"`
	// Args is *[]string so we can distinguish absent (nil pointer →
	// any argv) from explicitly empty (`"args": []` → exactly zero
	// elements). encoding/json sets the pointer to a real slice only
	// when the field is present.
	Args        *[]string `json:"args"`
	Timeout     string    `json:"timeout"`
	As          []string  `json:"as"`
	Description string    `json:"description"`
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
	if r.Path == "" {
		return CommandRule{}, fmt.Errorf("`path` is required")
	}
	if !strings.HasPrefix(r.Path, "/") {
		return CommandRule{}, fmt.Errorf("`path` must be absolute, got %q", r.Path)
	}
	rule := CommandRule{Path: r.Path}
	if r.Args != nil {
		// Compile one auto-anchored regex per argv element. The wrap
		// in ^(?:…)$ means MatchString — which is unanchored — cannot
		// silently allow a substring; idempotent if the operator
		// already wrote ^…$.
		patterns := make([]*regexp.Regexp, len(*r.Args))
		for i, src := range *r.Args {
			re, err := regexp.Compile("^(?:" + src + ")$")
			if err != nil {
				return CommandRule{}, fmt.Errorf("invalid `args[%d]` regex for %s: %w", i, r.Path, err)
			}
			patterns[i] = re
		}
		rule.ArgsPatterns = patterns
		rule.ArgsSource = append([]string(nil), *r.Args...)
	}
	if r.Timeout != "" {
		d, err := time.ParseDuration(r.Timeout)
		if err != nil {
			return CommandRule{}, fmt.Errorf("invalid `timeout` for %s: %w", r.Path, err)
		}
		rule.Timeout = d
	}
	as, err := normalizeAs(r.Path, r.As)
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
func normalizeAs(path string, as []string) ([]string, error) {
	if len(as) == 0 {
		return []string{SelfUser}, nil
	}
	seen := make(map[string]bool, len(as))
	out := make([]string, 0, len(as))
	for _, u := range as {
		if u == "" {
			return nil, fmt.Errorf("`as` for %s has an empty entry", path)
		}
		if seen[u] {
			return nil, fmt.Errorf("`as` for %s has duplicate entry %q", path, u)
		}
		if u != SelfUser && !validUsername.MatchString(u) {
			return nil, fmt.Errorf("`as` for %s has invalid username %q (expected POSIX login name)", path, u)
		}
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}
