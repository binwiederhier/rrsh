package config

import (
	"testing"
	"time"
)

func TestParse_Valid(t *testing.T) {
	data := []byte(`
timeout: 5s
commands:
  - /usr/bin/whoami
  - /usr/bin/ls: "^-la$"
  - /usr/bin/ping: { args: "^-c \\d+ .+$", timeout: 30s }
`)
	cfg, err := Parse(data)
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

func TestParse_DefaultTimeout(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/whoami
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", cfg.Timeout)
	}
}

func TestParse_InvalidRegex(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/ls: "[invalid"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	data := []byte(`{{{`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParse_AsDefaults(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/whoami
  - /usr/bin/ls: "^-la$"
  - /usr/bin/ping: { args: "^-c \\d+$" }
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, r := range cfg.Commands {
		if len(r.As) != 1 || r.As[0] != SelfUser {
			t.Errorf("command[%d] (%s).As = %v, want [self]", i, r.Path, r.As)
		}
	}
}

func TestParse_AsList(t *testing.T) {
	data := []byte(`
commands:
  - /bin/systemctl: { args: "^restart .+$", as: [root] }
  - /usr/bin/whoami: { as: [self, root] }
  - /bin/deploy.sh: { as: [self, deploy] }
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{
		{"root"},
		{"self", "root"},
		{"self", "deploy"},
	}
	for i, w := range want {
		got := cfg.Commands[i].As
		if len(got) != len(w) {
			t.Errorf("command[%d].As = %v, want %v", i, got, w)
			continue
		}
		for j := range w {
			if got[j] != w[j] {
				t.Errorf("command[%d].As[%d] = %q, want %q", i, j, got[j], w[j])
			}
		}
	}
}

func TestParse_AsRejectsScalar(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/whoami: { as: root }
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error: `as` must be a list, not a scalar")
	}
}

func TestParse_AsRejectsEmpty(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/whoami: { as: [] }
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error: `as` must not be empty")
	}
}

func TestParse_AsRejectsDuplicates(t *testing.T) {
	data := []byte(`
commands:
  - /usr/bin/whoami: { as: [self, self] }
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error: duplicate `as` entry")
	}
}

func TestParse_AllFormats(t *testing.T) {
	data := []byte(`
timeout: 10s
commands:
  - /usr/bin/whoami
  - /usr/bin/ls: "^-la$"
  - /usr/bin/ping: { args: "^-c \\d+$", timeout: 20s }
`)
	cfg, err := Parse(data)
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
