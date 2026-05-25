// Package config parses and validates the rrsh allowlist config.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"syscall"
	"time"
)

// Config is the parsed allowlist. See README for the JSON schema.
type Config struct {
	Instructions string
	Commands     []CommandRule
}

// CommandRule is one allowlist entry. CommandPatterns[0] matches the
// binary path; [1..N-1] match argv 1-for-1. CommandSource keeps the
// original regex strings for list_commands' response.
type CommandRule struct {
	CommandPatterns []*regexp.Regexp
	CommandSource   []string
	Timeout         time.Duration
	As              []string // empty = current user only
	Description     string
}

// Load reads and parses the config file. Refuses group/world-writable
// or non-root-owned files: a writable or rrsh-owned allowlist + sudoers
// grant = root escalation.
func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat config: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o022 != 0 {
		return nil, fmt.Errorf("refusing to load config %s: file is group/world-writable (mode %04o)", path, mode)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
		return nil, fmt.Errorf("refusing to load config %s: file must be owned by root (uid=%d)", path, st.Uid)
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
	// Auto-anchor with ^(?:...)$ so MatchString can't silently allow a
	// substring. Idempotent if the operator already wrote ^...$.
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
		CommandSource:   slices.Clone(r.Command),
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

// normalizeAs validates `as:`. Empty stays empty (interpreted by the
// matcher as "current user only"). pathHint is included in error
// messages so the operator can locate the bad rule.
func normalizeAs(pathHint string, as []string) ([]string, error) {
	if len(as) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(as))
	out := make([]string, 0, len(as))
	for _, u := range as {
		if u == "" {
			return nil, fmt.Errorf("`as` for %s has an empty entry", pathHint)
		} else if seen[u] {
			return nil, fmt.Errorf("`as` for %s has duplicate entry %q", pathHint, u)
		} else if !validUsername.MatchString(u) {
			return nil, fmt.Errorf("`as` for %s has invalid username %q (expected POSIX login name)", pathHint, u)
		}
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}
