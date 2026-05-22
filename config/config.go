package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/binwiederhier/rrsh/auth"
)

// Config is the parsed allowlist. See README for the JSON schema.
type Config struct {
	Instructions string
	Commands     []CommandRule
}

// CommandRule is one allowlist entry. CommandPatterns is the rule's
// structural spec: index 0 matches the binary path, indices 1..N-1
// match argv[0..N-2]. CommandSource holds the original regex strings
// for list_commands display.
type CommandRule struct {
	CommandPatterns []*regexp.Regexp
	CommandSource   []string
	Timeout         time.Duration
	As              []string // defaults to [auth.SelfUser]
	Description     string
}

// Load reads and parses the config file at path. Refuses to load when
// the file is group- or world-writable: a misconfigured /etc/rrsh/rrsh.json
// would let the rrsh user (or anyone in its group) rewrite the allowlist
// and, with the sudoers grant in place, escalate to root.
func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat config: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o022 != 0 {
		return nil, fmt.Errorf("refusing to load config %s: file is group/world-writable (mode %04o)", path, mode)
	}
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

// normalizeAs validates `as:` and defaults to ["self"]. pathHint goes
// into error messages so the operator can find the offending rule.
func normalizeAs(pathHint string, as []string) ([]string, error) {
	if len(as) == 0 {
		return []string{auth.SelfUser}, nil
	}
	seen := make(map[string]bool, len(as))
	out := make([]string, 0, len(as))
	for _, u := range as {
		if u == "" {
			return nil, fmt.Errorf("`as` for %s has an empty entry", pathHint)
		} else if seen[u] {
			return nil, fmt.Errorf("`as` for %s has duplicate entry %q", pathHint, u)
		} else if u != auth.SelfUser && !validUsername.MatchString(u) {
			return nil, fmt.Errorf("`as` for %s has invalid username %q (expected POSIX login name)", pathHint, u)
		}
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}
