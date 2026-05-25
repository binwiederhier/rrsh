package audit

import (
	"strings"
	"testing"
)

// TestFormatEvent_Basic covers the canonical log shape.
func TestFormatEvent_Basic(t *testing.T) {
	t.Parallel()
	got := formatEvent("ALLOWED", "rrsh", "/usr/bin/whoami")
	want := "ALLOWED: user=rrsh cmd=/usr/bin/whoami"
	if got != want {
		t.Errorf("formatEvent = %q, want %q", got, want)
	}
}

// TestFormatEvent_EscapesUser covers the syslog-injection mitigation:
// the user field is escaped so a value like "root\nALLOWED: ..." cannot
// forge a fake audit record.
func TestFormatEvent_EscapesUser(t *testing.T) {
	t.Parallel()
	got := formatEvent("DENIED", "root\nALLOWED: user=root cmd=/bin/sh", "/bin/x")
	if strings.ContainsAny(got, "\n\r\x00") {
		t.Errorf("formatEvent leaked raw control bytes: %q", got)
	}
	if !strings.Contains(got, `user=root\nALLOWED`) {
		t.Errorf("formatEvent should escape newline in user, got: %q", got)
	}
}
