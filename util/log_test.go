package util

import (
	"strings"
	"testing"
)

// TestJoinForLog_EscapesControlChars covers the log-injection mitigation
// shared by both the privileged half (cmd/sudo.go) and the JSON-RPC
// server (server/server.go) - newlines, CRs and NUL bytes in command
// elements must not be passed verbatim into syslog, or an authenticated
// caller could forge fake log records that look like legitimate
// ALLOWED/DENIED entries.
func TestJoinForLog_EscapesControlChars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command []string
		want    string
	}{
		{"newline", []string{"/bin/echo", "a\nALLOWED: root cmd=/bin/sh"}, "/bin/echo a\\nALLOWED: root cmd=/bin/sh"},
		{"cr", []string{"/bin/echo", "a\rb"}, "/bin/echo a\\rb"},
		{"nul", []string{"/bin/echo", "a\x00b"}, "/bin/echo a\\0b"},
		{"clean", []string{"/bin/echo", "hello", "world"}, "/bin/echo hello world"},
		{"path with newline", []string{"/bin/x\n"}, "/bin/x\\n"},
		{"no argv", []string{"/bin/whoami"}, "/bin/whoami"},
		{"esc", []string{"/bin/echo", "\x1b[2J"}, "/bin/echo \\x1b[2J"},
		{"bel", []string{"/bin/echo", "a\x07b"}, "/bin/echo a\\x07b"},
		{"del", []string{"/bin/echo", "a\x7fb"}, "/bin/echo a\\x7fb"},
		{"tab kept", []string{"/bin/echo", "a\tb"}, "/bin/echo a\tb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := JoinForLog(tc.command)
			if got != tc.want {
				t.Errorf("JoinForLog = %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "\n\r\x00\x1b\x07\x7f") {
				t.Errorf("result still contains raw control bytes: %q", got)
			}
		})
	}
}
