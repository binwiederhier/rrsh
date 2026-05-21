package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"
)

// SelfUser is the magic token in `as:` lists meaning "the SSH user who
// invoked rrsh". Resolved at runtime against $SUDO_USER (privileged
// subcommand) or the current user (unprivileged process).
const SelfUser = "self"

// CommandRule is one allowlist entry. CommandPatterns is the rule's
// structural spec: index 0 matches the binary path, indices 1..N-1
// match argv[0..N-2]. CommandSource holds the original regex strings
// for list_commands display.
type CommandRule struct {
	CommandPatterns []*regexp.Regexp
	CommandSource   []string
	Timeout         time.Duration
	As              []string // defaults to [SelfUser]
	Description     string
}

// Config is the parsed allowlist. See README for the JSON schema.
//
// Sudo is the master switch for elevation: even with the sudoers grant
// in place, every `as:`-other-than-self call is denied until Sudo=true.
type Config struct {
	Instructions string
	Sudo         bool
	Commands     []CommandRule
}

type rawConfig struct {
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
		Instructions: raw.Instructions,
		Sudo:         raw.Sudo,
		Commands:     make([]CommandRule, 0, len(raw.Commands)),
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
	// Auto-anchor each pattern with ^(?:…)$ so MatchString (unanchored
	// by default) can't silently allow a substring. Idempotent if the
	// operator already wrote ^…$.
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

// validUsername is a conservative POSIX login-name pattern: lowercase
// letter or underscore start, then alnum/underscore/dash, up to 32
// chars, optionally trailing `$` (Samba). Rejects names that would
// look like sudo flags ("-h", "--", "-u").
var validUsername = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)

// normalizeAs validates `as:` and defaults to ["self"]. pathHint goes
// into error messages so the operator can find the offending rule.
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
