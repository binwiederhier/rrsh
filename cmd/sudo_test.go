package cmd

import (
	"reflect"
	"strings"
	"testing"
)

// TestJoinForLog_EscapesControlChars covers the same log-injection
// mitigation as the mcp-package copy, but for the privileged half. Both
// copies must escape control chars so a syslog reader can't be fooled
// by argv containing newlines.
func TestJoinForLog_EscapesControlChars(t *testing.T) {
	t.Parallel()
	got := joinForLog("/bin/echo", []string{"a\nALLOWED: root cmd=/bin/sh", "b\r\x00c"})
	if got != "/bin/echo a\\nALLOWED: root cmd=/bin/sh b\\r\\0c" {
		t.Errorf("got %q", got)
	}
	for _, c := range "\n\r\x00" {
		if strings.ContainsRune(got, c) {
			t.Errorf("result still contains raw control rune %q", c)
		}
	}
}

func TestResolveAllowedUsers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		allowed []string
		self    string
		want    []string
	}{
		{"empty list", nil, "ai", []string{}},
		{"self resolves to current user", []string{"self"}, "ai", []string{"ai"}},
		{"plain users pass through", []string{"root", "deploy"}, "ai", []string{"root", "deploy"}},
		{"mixed list", []string{"self", "root"}, "ai", []string{"ai", "root"}},
	}
	for _, tc := range tests {
		got := resolveAllowedUsers(tc.allowed, tc.self)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: resolveAllowedUsers(%v, %q) = %v, want %v",
				tc.name, tc.allowed, tc.self, got, tc.want)
		}
	}
}
