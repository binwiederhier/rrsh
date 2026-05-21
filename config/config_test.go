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
			{ "path": "/usr/bin/whoami" },
			{ "path": "/usr/bin/ls",   "args": ["-la"] },
			{ "path": "/usr/bin/ping", "args": ["-c", "\\d+", ".+"], "timeout": "60s" }
		]
	}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Commands) != 3 {
		t.Fatalf("got %d commands, want 3", len(cfg.Commands))
	}
	// No-args rule: ArgsPatterns and ArgsSource both nil.
	if cfg.Commands[0].Path != "/usr/bin/whoami" || cfg.Commands[0].ArgsPatterns != nil {
		t.Errorf("command[0] = %+v", cfg.Commands[0])
	}
	// One-element rule: behavior check (auto-anchor in effect).
	if len(cfg.Commands[1].ArgsPatterns) != 1 {
		t.Fatalf("command[1] should have 1 pattern, got %d", len(cfg.Commands[1].ArgsPatterns))
	}
	if !cfg.Commands[1].ArgsPatterns[0].MatchString("-la") {
		t.Errorf("command[1] pattern should match \"-la\"")
	}
	if cfg.Commands[1].ArgsPatterns[0].MatchString("-la /etc/passwd") {
		t.Errorf("command[1] pattern should not match \"-la /etc/passwd\" (auto-anchored)")
	}
	// Three-element rule with timeout.
	if len(cfg.Commands[2].ArgsPatterns) != 3 {
		t.Errorf("command[2] should have 3 patterns, got %d", len(cfg.Commands[2].ArgsPatterns))
	}
	if cfg.Commands[2].Timeout != 60*time.Second {
		t.Errorf("command[2].Timeout = %v, want 60s", cfg.Commands[2].Timeout)
	}
}

// TestParse_EmptyArgsListMeansZeroArgv distinguishes the two "no args"
// states: an absent `args` field allows any argv, while `"args": []`
// explicitly requires zero argv elements.
func TestParse_EmptyArgsListMeansZeroArgv(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"path": "/usr/bin/x", "args": []}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commands[0].ArgsPatterns == nil {
		t.Error("explicit empty args list should produce non-nil ArgsPatterns (length 0)")
	}
	if len(cfg.Commands[0].ArgsPatterns) != 0 {
		t.Errorf("expected 0 patterns, got %d", len(cfg.Commands[0].ArgsPatterns))
	}
}

// TestParse_AutoAnchorsArgsRegex proves that each per-element regex is
// wrapped in ^(?:…)$ so an operator who writes "ntfy" without anchors
// can't accidentally accept "ntfy-something" as an argv element.
func TestParse_AutoAnchorsArgsRegex(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"path": "/usr/bin/x", "args": ["restart", "ntfy"]}
	]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patterns := cfg.Commands[0].ArgsPatterns
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(patterns))
	}
	// The intended values match.
	if !patterns[0].MatchString("restart") || !patterns[1].MatchString("ntfy") {
		t.Errorf("intended argv should match its per-element patterns")
	}
	// Pre-fix vulnerability: extra characters in an element used to slip
	// through because MatchString is unanchored. Auto-anchor blocks them.
	if patterns[0].MatchString("restart-foo") {
		t.Errorf("\"restart-foo\" should NOT match auto-anchored \"restart\"")
	}
	if patterns[1].MatchString("ntfy; reboot") {
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
		{"leading-dash", `{"commands":[{"path":"/bin/x","as":["-h"]}]}`},
		{"double-dash", `{"commands":[{"path":"/bin/x","as":["--"]}]}`},
		{"with-space", `{"commands":[{"path":"/bin/x","as":["root user"]}]}`},
		{"with-comma", `{"commands":[{"path":"/bin/x","as":["root,deploy"]}]}`},
		{"uppercase", `{"commands":[{"path":"/bin/x","as":["Root"]}]}`},
		{"too-long", `{"commands":[{"path":"/bin/x","as":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}]}`},
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
		{"path":"/bin/x","as":["self","root","deploy","_apt","www-data","host$"]}
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
	// "timeout" is no longer a top-level key; the strict parser must
	// flag it as unknown rather than silently accepting it.
	_, err := Parse([]byte(`{"timeout": "5s", "commands": []}`))
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected unknown-field error for top-level timeout, got: %v", err)
	}
}

func TestParse_RejectsBadPerCommandTimeoutValue(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "timeout": "nope"}]}`))
	if err == nil {
		t.Fatal("expected error for bad per-command timeout")
	}
}

func TestParse_Description(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"commands": [
		{"path": "/usr/bin/whoami", "description": "Show effective username."},
		{"path": "/bin/systemctl", "args": ["restart", "ntfy"], "as": ["root"]}
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
	cfg, err := Parse([]byte(`{"commands": [{"path": "/usr/bin/whoami"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Sudo {
		t.Error("Sudo should default to false")
	}
}

func TestParse_SudoTrue(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`{"sudo": true, "commands": [{"path": "/usr/bin/whoami"}]}`))
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
		{"path": "/usr/bin/whoami"},
		{"path": "/usr/bin/ls", "args": ["-la"]}
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
		{"path": "/bin/systemctl",     "args": ["restart", ".+"], "as": ["root"]},
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
	t.Parallel()
	_, err := Parse([]byte(`{"bogus": true, "commands": []}`))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestParse_RejectsUnknownRuleField(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "junk": 1}]}`))
	if err == nil || !strings.Contains(err.Error(), "junk") {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestParse_RejectsRelativePath(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"path": "whoami"}]}`))
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path error, got: %v", err)
	}
}

func TestParse_RejectsMissingPath(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"args": ["x"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected path-required error, got: %v", err)
	}
}

func TestParse_RejectsBadRegex(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "args": ["[invalid"]}]}`))
	if err == nil {
		t.Fatal("expected error for bad regex")
	}
}

func TestParse_RejectsAsDuplicates(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "as": ["root", "root"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate-as error, got: %v", err)
	}
}

func TestParse_RejectsAsEmptyString(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`{"commands": [{"path": "/bin/x", "as": [""]}]}`))
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
