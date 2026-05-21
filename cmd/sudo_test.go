package cmd

import (
	"strings"
	"testing"
)

// TestJoinForLog_EscapesControlChars covers the log-injection mitigation
// in the privileged half - argv with embedded newlines must not forge
// fake audit-log entries.
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
