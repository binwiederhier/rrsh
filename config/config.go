// Package config parses rrsh's allowlist configuration (JSON).
//
// Schema:
//
//	{
//	  "timeout": "10s",
//	  "commands": [
//	    { "path": "/usr/bin/whoami" },
//	    { "path": "/usr/bin/ls",     "args": "^-la /var/log/.*$" },
//	    { "path": "/usr/bin/ping",   "args": "^-c \\d+ .+$", "timeout": "30s" },
//	    { "path": "/bin/systemctl",  "args": "^restart ntfy$", "as": ["root"] },
//	    { "path": "/usr/bin/journalctl", "args": "^-u ntfy( -f)?$", "as": ["self", "root"] }
//	  ]
//	}
//
// Fields on each command entry:
//
//   - path     absolute path to the binary (required)
//   - args     regex the argument string must match (default: any args allowed)
//   - timeout  per-command timeout, e.g. "30s" (default: global timeout)
//   - as       list of users the command may run as (default: ["self"])
//
// `self` in an `as` list resolves to the SSH user at runtime. Other entries are
// real usernames (e.g. "root", "deploy"). Unknown fields are rejected — the
// parser is strict because it sits on the privileged trust boundary.
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

// DefaultTimeout is applied when the config omits a top-level `timeout`.
const DefaultTimeout = 10 * time.Second

// SelfUser is the magic token in `as` lists meaning "the SSH user who invoked
// rrsh". Resolved at runtime against $SUDO_USER (in the privileged
// subcommand) or the current user (in the unprivileged process).
const SelfUser = "self"

type CommandRule struct {
	Path        string
	ArgsPattern *regexp.Regexp
	Timeout     time.Duration
	// As lists the users the command may run as. SelfUser ("self") is the
	// SSH user. Other entries are real usernames (e.g. "root", "deploy").
	// Defaults to [SelfUser] when omitted in the config.
	As []string
}

type Config struct {
	Timeout  time.Duration
	Commands []CommandRule
}

// rawConfig mirrors the on-disk JSON shape. Strings for timeout/args keep the
// JSON readable; we convert + validate after unmarshal.
type rawConfig struct {
	Timeout  string    `json:"timeout"`
	Commands []rawRule `json:"commands"`
}

type rawRule struct {
	Path    string   `json:"path"`
	Args    string   `json:"args"`
	Timeout string   `json:"timeout"`
	As      []string `json:"as"`
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

	cfg := &Config{Timeout: DefaultTimeout}
	if raw.Timeout != "" {
		d, err := time.ParseDuration(raw.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q: %w", raw.Timeout, err)
		}
		cfg.Timeout = d
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
	if r.Args != "" {
		re, err := regexp.Compile(r.Args)
		if err != nil {
			return CommandRule{}, fmt.Errorf("invalid `args` regex for %s: %w", r.Path, err)
		}
		rule.ArgsPattern = re
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
	return rule, nil
}

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
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}
