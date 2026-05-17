package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultTimeout is applied when the YAML omits the global `timeout` key.
const DefaultTimeout = 10 * time.Second

// SelfUser is the magic token in `as:` lists meaning "the SSH user who invoked
// noshell". Resolved at runtime against $SUDO_USER (in the privileged subcommand)
// or the current user (in the unprivileged process).
const SelfUser = "self"

type CommandRule struct {
	Path        string
	ArgsPattern *regexp.Regexp
	Timeout     time.Duration
	// As lists the users the command may run as. SelfUser ("self") is the
	// SSH user. Other entries are real usernames (e.g. "root", "deploy").
	// Defaults to [SelfUser] when omitted in the YAML.
	As []string
}

type Config struct {
	Timeout  time.Duration
	Commands []CommandRule
}

type rawConfig struct {
	Timeout  string        `yaml:"timeout"`
	Commands []interface{} `yaml:"commands"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	cfg := &Config{}

	if raw.Timeout != "" {
		d, err := time.ParseDuration(raw.Timeout)
		if err != nil {
			return nil, fmt.Errorf("parsing timeout %q: %w", raw.Timeout, err)
		}
		cfg.Timeout = d
	} else {
		cfg.Timeout = DefaultTimeout
	}

	for _, entry := range raw.Commands {
		rule, err := parseCommandEntry(entry)
		if err != nil {
			return nil, err
		}
		cfg.Commands = append(cfg.Commands, rule)
	}

	return cfg, nil
}

func parseCommandEntry(entry interface{}) (CommandRule, error) {
	rule, err := parseCommandEntryRaw(entry)
	if err != nil {
		return CommandRule{}, err
	}
	if len(rule.As) == 0 {
		rule.As = []string{SelfUser}
	}
	return rule, nil
}

func parseCommandEntryRaw(entry interface{}) (CommandRule, error) {
	switch v := entry.(type) {
	case string:
		// Simple: "- /usr/bin/whoami" (no args restriction)
		return CommandRule{Path: v}, nil

	case map[string]interface{}:
		if len(v) != 1 {
			return CommandRule{}, fmt.Errorf("command entry must have exactly one key, got %d", len(v))
		}
		for path, val := range v {
			return parseCommandValue(path, val)
		}
	}
	return CommandRule{}, fmt.Errorf("unexpected command entry type: %T", entry)
}

func parseCommandValue(path string, val interface{}) (CommandRule, error) {
	switch v := val.(type) {
	case nil:
		// "- /usr/bin/whoami:" with no value
		return CommandRule{Path: path}, nil

	case string:
		// "- /usr/bin/ls: "^-la /var/log/.*$""
		re, err := regexp.Compile(v)
		if err != nil {
			return CommandRule{}, fmt.Errorf("invalid regex for %s: %w", path, err)
		}
		return CommandRule{Path: path, ArgsPattern: re}, nil

	case map[string]interface{}:
		// Map with args and/or timeout and/or as
		rule := CommandRule{Path: path}

		if args, ok := v["args"]; ok {
			argsStr, ok := args.(string)
			if !ok {
				return CommandRule{}, fmt.Errorf("args for %s must be a string", path)
			}
			re, err := regexp.Compile(argsStr)
			if err != nil {
				return CommandRule{}, fmt.Errorf("invalid regex for %s: %w", path, err)
			}
			rule.ArgsPattern = re
		}

		if t, ok := v["timeout"]; ok {
			tStr, ok := t.(string)
			if !ok {
				return CommandRule{}, fmt.Errorf("timeout for %s must be a string", path)
			}
			d, err := time.ParseDuration(tStr)
			if err != nil {
				return CommandRule{}, fmt.Errorf("invalid timeout for %s: %w", path, err)
			}
			rule.Timeout = d
		}

		if a, ok := v["as"]; ok {
			asList, err := parseAsList(path, a)
			if err != nil {
				return CommandRule{}, err
			}
			rule.As = asList
		}

		return rule, nil
	}

	return CommandRule{}, fmt.Errorf("unexpected value type for %s: %T", path, val)
}

// parseAsList enforces that `as:` is always a YAML list of usernames. The
// always-list form keeps the parser uniform and avoids a string/list ambiguity
// at every consumer.
func parseAsList(path string, val interface{}) ([]string, error) {
	raw, ok := val.([]interface{})
	if !ok {
		return nil, fmt.Errorf("`as` for %s must be a list (e.g. [self, root])", path)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("`as` for %s must not be empty", path)
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("`as` items for %s must be strings, got %T", path, item)
		}
		if seen[s] {
			return nil, fmt.Errorf("`as` for %s has duplicate entry %q", path, s)
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}
