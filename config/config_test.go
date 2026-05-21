package config

import (
	"strings"
	"testing"
	"time"
)

func TestParse_Valid(t *testing.T) {
	t.Parallel()
	data := []byte(`{
		"commands": [
			{ "command": ["/usr/bin/whoami"] },
			{ "command": ["/usr/bin/ls", "-la"] },
			{ "command": ["/usr/bin/ping", "-c", "\\d+", ".+"], "timeout": "60s" }
		]
	}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Commands) != 3 {
		t.Fatalf("got %d commands, want 3", len(cfg.Commands))
	}
	// Zero-args rule: only the path regex.
	if len(cfg.Commands[0].CommandPatterns) != 1 {
		t.Errorf("command[0] should have 1 pattern (just the path), got %d", len(cfg.Commands[0].CommandPatterns))
	}
	if !cfg.Commands[0].CommandPatterns[0].MatchString("/usr/bin/whoami") {
		t.Errorf("command[0] path pattern should match /usr/bin/whoami")
	}
	// Two-element rule: path + one argv regex (auto-anchored).
	if len(cfg.Commands[1].CommandPatterns) != 2 {
		t.Fatalf("command[1] should have 2 patterns, got %d", len(cfg.Commands[1].CommandPatterns))
	}
	if !cfg.Commands[1].CommandPatterns[1].MatchString("-la") {
		t.Errorf("command[1] argv pattern should match \"-la\"")
	}
	if cfg.Commands[1].CommandPatterns[1].MatchString("-la /etc/passwd") {
		t.Errorf("command[1] argv pattern should not match \"-la /etc/passwd\" (auto-anchored)")
	}
	// Four-element rule with timeout (path + 3 args).
	if len(cfg.Commands[2].CommandPatterns) != 4 {
		t.Errorf("command[2] should have 4 patterns, got %d", len(cfg.Commands[2].CommandPatterns))
	}
	if cfg.Commands[2].Timeout != 60*time.Second {
		t.Errorf("command[2].Timeout = %v, want 60s", cfg.Commands[2].Timeout)
	}
}

// TestParse_PathOnlyRequiresZeroArgv verifies that a single-element
// command (just the path regex) means "exactly zero argv elements" —
// not "any argv shape."
func TestParse_PathOnlyRequiresZeroArgv(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"command": ["/usr/bin/whoami"]}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Commands[0].CommandPatterns) != 1 {
		t.Errorf("expected 1 pattern (path only), got %d", len(cfg.Commands[0].CommandPatterns))
	}
	if len(cfg.Commands[0].CommandSource) != 1 {
		t.Errorf("expected 1 source entry, got %d", len(cfg.Commands[0].CommandSource))
	}
}

// TestParse_AutoAnchorsCommandRegex proves that every command-list entry
// is wrapped in ^(?:…)$ so unanchored regexes can't match substrings.
func TestParse_AutoAnchorsCommandRegex(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"command": ["/usr/bin/x", "restart", "ntfy"]}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patterns := cfg.Commands[0].CommandPatterns
	if len(patterns) != 3 {
		t.Fatalf("expected 3 patterns (path + 2 args), got %d", len(patterns))
	}
	// Path matches exactly.
	if !patterns[0].MatchString("/usr/bin/x") {
		t.Errorf("path should match")
	}
	if patterns[0].MatchString("/usr/bin/x-extra") {
		t.Errorf("path should NOT match \"/usr/bin/x-extra\" (auto-anchored)")
	}
	// argv element regexes match the intended value but not extras.
	if !patterns[1].MatchString("restart") || !patterns[2].MatchString("ntfy") {
		t.Errorf("intended argv values should match")
	}
	if patterns[1].MatchString("restart-foo") {
		t.Errorf("\"restart-foo\" should NOT match auto-anchored \"restart\"")
	}
	if patterns[2].MatchString("ntfy; reboot") {
		t.Errorf("\"ntfy; reboot\" should NOT match auto-anchored \"ntfy\"")
	}
}

// TestParse_RejectsInvalidUsernameInAs covers fix #5 — usernames that
// could be confused for sudo flags or that aren't valid POSIX login
// names must be rejected at parse time.
func TestParse_RejectsInvalidUsernameInAs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		json string
	}{
		{"leading-dash", `{"commands":[{"command":["/bin/x"],"as":["-h"]}]}`},
		{"double-dash", `{"commands":[{"command":["/bin/x"],"as":["--"]}]}`},
		{"with-space", `{"commands":[{"command":["/bin/x"],"as":["root user"]}]}`},
		{"with-comma", `{"commands":[{"command":["/bin/x"],"as":["root,deploy"]}]}`},
		{"uppercase", `{"commands":[{"command":["/bin/x"],"as":["Root"]}]}`},
		{"too-long", `{"commands":[{"command":["/bin/x"],"as":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tc.json))
			if err == nil {
				t.Errorf("expected error for invalid username, got none")
			}
		})
	}
}

func TestParse_AcceptsValidUsernameInAs(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands":[
		{"command":["/bin/x"],"as":["self","root","deploy","_apt","www-data","host$"]}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"self", "root", "deploy", "_apt", "www-data", "host$"}
	if !equalStrings(cfg.Commands[0].As, want) {
		t.Errorf("As = %v, want %v", cfg.Commands[0].As, want)
	}
}

func TestParse_TopLevelTimeoutRejected(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"timeout": "5s", "commands": []}`))
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected unknown-field error for top-level timeout, got: %v", err)
	}
}

func TestParse_RejectsBadPerCommandTimeoutValue(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"command": ["/bin/x"], "timeout": "nope"}]}`))
	if err == nil {
		t.Fatal("expected error for bad per-command timeout")
	}
}

func TestParse_Description(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"command": ["/usr/bin/whoami"], "description": "Show effective username."},
		{"command": ["/bin/systemctl", "restart", "ntfy"], "as": ["root"]}
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
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [{"command": ["/usr/bin/whoami"]}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Sudo {
		t.Error("Sudo should default to false")
	}
}

func TestParse_SudoTrue(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"sudo": true, "commands": [{"command": ["/usr/bin/whoami"]}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Sudo {
		t.Error("Sudo should be true when explicitly set")
	}
}

func TestParse_AsDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"command": ["/usr/bin/whoami"]},
		{"command": ["/usr/bin/ls", "-la"]}
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
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"command": ["/bin/systemctl", "restart", ".+"], "as": ["root"]},
		{"command": ["/usr/bin/whoami"],                 "as": ["self", "root"]},
		{"command": ["/bin/deploy.sh"],                  "as": ["self", "deploy"]}
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
	t.Parallel()
	_, err := Parse([]byte(`{"bogus": true, "commands": []}`))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestParse_RejectsUnknownRuleField(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"command": ["/bin/x"], "junk": 1}]}`))
	if err == nil || !strings.Contains(err.Error(), "junk") {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestParse_RejectsMissingCommand(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"description": "no command"}]}`))
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("expected command-required error, got: %v", err)
	}
}

func TestParse_RejectsEmptyCommand(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"command": []}]}`))
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("expected non-empty-command error, got: %v", err)
	}
}

func TestParse_RejectsBadRegex(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"command": ["/bin/x", "[invalid"]}]}`))
	if err == nil {
		t.Fatal("expected error for bad regex")
	}
}

func TestParse_RejectsAsDuplicates(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"command": ["/bin/x"], "as": ["root", "root"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate-as error, got: %v", err)
	}
}

func TestParse_RejectsAsEmptyString(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"command": ["/bin/x"], "as": [""]}]}`))
	if err == nil {
		t.Fatal("expected error for empty as entry")
	}
}

func TestParse_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
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
