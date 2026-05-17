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

type CommandRule struct {
	Path        string
	ArgsPattern *regexp.Regexp
	Timeout     time.Duration
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
		// Map with args and/or timeout
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

		return rule, nil
	}

	return CommandRule{}, fmt.Errorf("unexpected value type for %s: %T", path, val)
}
