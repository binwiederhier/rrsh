package config

import (
	"strings"
	"testing"
	"time"
)

func TestParse_Valid(t *testing.T) {
	data := []byte(`{
		"timeout": "5s",
		"commands": [
			{ "path": "/usr/bin/whoami" },
			{ "path": "/usr/bin/ls",   "args": "^-la$" },
			{ "path": "/usr/bin/ping", "args": "^-c \\d+ .+$", "timeout": "30s" }
		]
	}`)
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
	if cfg.Commands[0].Path != "/usr/bin/whoami" || cfg.Commands[0].ArgsPattern != nil {
		t.Errorf("command[0] = %+v", cfg.Commands[0])
	}
	if cfg.Commands[1].ArgsPattern == nil || cfg.Commands[1].ArgsPattern.String() != "^-la$" {
		t.Errorf("command[1].ArgsPattern = %v", cfg.Commands[1].ArgsPattern)
	}
	if cfg.Commands[2].Timeout != 30*time.Second {
		t.Errorf("command[2].Timeout = %v, want 30s", cfg.Commands[2].Timeout)
	}
}

func TestParse_DefaultTimeout(t *testing.T) {
	cfg, err := Parse([]byte(`{"commands": [{"path": "/usr/bin/whoami"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", cfg.Timeout)
	}
}

func TestParse_Description(t *testing.T) {
	cfg, err := Parse([]byte(`{"commands": [
		{"path": "/usr/bin/whoami", "description": "Show effective username."},
		{"path": "/bin/systemctl", "args": "^restart ntfy$", "as": ["root"]}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commands[0].Description != "Show effective username." {
		t.Errorf("command[0].Description = %q, want %q", cfg.Commands[0].Description, "Show effective username.")
	}
	if cfg.Commands[1].Description != "" {
		t.Errorf("command[1].Description = %q, want empty", cfg.Commands[1].Description)
	}
}

func TestParse_SudoDefault(t *testing.T) {
	cfg, err := Parse([]byte(`{"commands": [{"path": "/usr/bin/whoami"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Sudo {
		t.Error("Sudo should default to false")
	}
}

func TestParse_SudoTrue(t *testing.T) {
	cfg, err := Parse([]byte(`{"sudo": true, "commands": [{"path": "/usr/bin/whoami"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Sudo {
		t.Error("Sudo should be true when explicitly set")
	}
}

func TestParse_AsDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`{"commands": [
		{"path": "/usr/bin/whoami"},
		{"path": "/usr/bin/ls", "args": "^-la$"}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, r := range cfg.Commands {
		if len(r.As) != 1 || r.As[0] != SelfUser {
			t.Errorf("command[%d].As = %v, want [self]", i, r.As)
		}
	}
}

func TestParse_AsList(t *testing.T) {
	cfg, err := Parse([]byte(`{"commands": [
		{"path": "/bin/systemctl",     "args": "^restart .+$", "as": ["root"]},
		{"path": "/usr/bin/whoami",    "as": ["self", "root"]},
		{"path": "/bin/deploy.sh",     "as": ["self", "deploy"]}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"root"}, {"self", "root"}, {"self", "deploy"}}
	for i, w := range want {
		got := cfg.Commands[i].As
		if !equalStrings(got, w) {
			t.Errorf("command[%d].As = %v, want %v", i, got, w)
		}
	}
}

func TestParse_RejectsUnknownTopLevelField(t *testing.T) {
	_, err := Parse([]byte(`{"timeout": "5s", "bogus": true, "commands": []}`))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestParse_RejectsUnknownRuleField(t *testing.T) {
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "junk": 1}]}`))
	if err == nil || !strings.Contains(err.Error(), "junk") {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestParse_RejectsRelativePath(t *testing.T) {
	_, err := Parse([]byte(`{"commands": [{"path": "whoami"}]}`))
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path error, got: %v", err)
	}
}

func TestParse_RejectsMissingPath(t *testing.T) {
	_, err := Parse([]byte(`{"commands": [{"args": "^x$"}]}`))
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected path-required error, got: %v", err)
	}
}

func TestParse_RejectsBadRegex(t *testing.T) {
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "args": "[invalid"}]}`))
	if err == nil {
		t.Fatal("expected error for bad regex")
	}
}

func TestParse_RejectsBadTimeout(t *testing.T) {
	_, err := Parse([]byte(`{"timeout": "nope", "commands": []}`))
	if err == nil {
		t.Fatal("expected error for bad timeout")
	}
}

func TestParse_RejectsBadPerCommandTimeout(t *testing.T) {
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "timeout": "nope"}]}`))
	if err == nil {
		t.Fatal("expected error for bad per-command timeout")
	}
}

func TestParse_RejectsAsDuplicates(t *testing.T) {
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "as": ["root", "root"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate-as error, got: %v", err)
	}
}

func TestParse_RejectsAsEmptyString(t *testing.T) {
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "as": [""]}]}`))
	if err == nil {
		t.Fatal("expected error for empty as entry")
	}
}

func TestParse_RejectsMalformedJSON(t *testing.T) {
	_, err := Parse([]byte(`{{{`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
