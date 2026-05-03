package main

import (
	"testing"
	"time"
)

func TestParseConfig_Valid(t *testing.T) {
	data := []byte(`
timeout: 5s
commands:
  - /usr/bin/whoami
  - /usr/bin/ls: "^-la$"
  - /usr/bin/ping: { args: "^-c \\d+ .+$", timeout: 30s }
`)
	cfg, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", cfg.Timeout)
	}

	if len(cfg.Commands) != 3 {
		t.Fatalf("got %d commands, want 3", len(cfg.Commands))
	}

	// Simple command, no args restriction
	if cfg.Commands[0].Path != "/usr/bin/whoami" {
		t.Errorf("command[0].Path = %q, want /usr/bin/whoami", cfg.Commands[0].Path)
	}
	if cfg.Commands[0].ArgsPattern != nil {
		t.Errorf("command[0].ArgsPattern should be nil")
	}

	// String args pattern
	if cfg.Commands[1].Path != "/usr/bin/ls" {
		t.Errorf("command[1].Path = %q, want /usr/bin/ls", cfg.Commands[1].Path)
	}
	if cfg.Commands[1].ArgsPattern == nil || cfg.Commands[1].ArgsPattern.String() != "^-la$" {
		t.Errorf("command[1].ArgsPattern = %v, want ^-la$", cfg.Commands[1].ArgsPattern)
	}

	// Map with args and timeout
	if cfg.Commands[2].Path != "/usr/bin/ping" {
		t.Errorf("command[2].Path = %q, want /usr/bin/ping", cfg.Commands[2].Path)
	}
	if cfg.Commands[2].ArgsPattern == nil {
		t.Fatal("command[2].ArgsPattern should not be nil")
	}
	if cfg.Commands[2].Timeout != 30*time.Second {
		t.Errorf("command[2].Timeout = %v, want 30s", cfg.Commands[2].Timeout)
	}
}

func TestParseConfig_DefaultTimeout(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/whoami
`)
	cfg, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", cfg.Timeout)
	}
}

func TestParseConfig_InvalidRegex(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/ls: "[invalid"
`)
	_, err := ParseConfig(data)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestParseConfig_InvalidYAML(t *testing.T) {
	data := []byte(`{{{`)
	_, err := ParseConfig(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseConfig_AllFormats(t *testing.T) {
	data := []byte(`
timeout: 10s
commands:
  - /usr/bin/whoami
  - /usr/bin/ls: "^-la$"
  - /usr/bin/ping: { args: "^-c \\d+$", timeout: 20s }
`)
	cfg, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Commands) != 3 {
		t.Fatalf("got %d commands, want 3", len(cfg.Commands))
	}

	// No restriction
	if cfg.Commands[0].ArgsPattern != nil {
		t.Error("whoami should have no args pattern")
	}

	// String restriction
	if cfg.Commands[1].ArgsPattern == nil {
		t.Error("ls should have args pattern")
	}

	// Map restriction with timeout
	if cfg.Commands[2].ArgsPattern == nil {
		t.Error("ping should have args pattern")
	}
	if cfg.Commands[2].Timeout != 20*time.Second {
		t.Errorf("ping timeout = %v, want 20s", cfg.Commands[2].Timeout)
	}
}
