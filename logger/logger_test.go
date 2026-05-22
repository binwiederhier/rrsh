package logger

import (
	"strings"
	"testing"
)

// TestFormatEvent_EscapesAsUser covers the syslog-injection mitigation:
// an authenticated JSON-RPC client controls the `as:` request field
// before any validation, so a value like "root\nALLOWED: ..." would
// otherwise forge a fake audit record on the DENIED path.
func TestFormatEvent_EscapesAsUser(t *testing.T) {
	t.Parallel()
	got := formatEvent("DENIED", "tester", "root\nALLOWED: user=root cmd=/bin/sh", "", "/bin/x")
	if strings.ContainsAny(got, "\n\r\x00") {
		t.Errorf("formatEvent leaked raw control bytes: %q", got)
	}
	if !strings.Contains(got, `as=root\nALLOWED`) {
		t.Errorf("formatEvent should escape newline in asUser, got: %q", got)
	}
}

// TestFormatEvent_OmitsAsUserWhenSameAsUser keeps the un-elevated case
// uncluttered: equal user/asUser collapses to a single user= field.
func TestFormatEvent_OmitsAsUserWhenSameAsUser(t *testing.T) {
	t.Parallel()
	got := formatEvent("ALLOWED", "tester", "tester", "", "/bin/whoami")
	if strings.Contains(got, "as=") {
		t.Errorf("formatEvent should omit as= when equal to user, got: %q", got)
	}
}

// TestFormatEvent_ShowsAsUserWhenElevated covers the original audit-gap
// fix: when a pipeline request elevates to root, the syslog line on the
// SSH-user side must surface as=root rather than collapse to just
// user=tester.
func TestFormatEvent_ShowsAsUserWhenElevated(t *testing.T) {
	t.Parallel()
	got := formatEvent("ALLOWED", "tester", "root", "", "/bin/whoami")
	if !strings.Contains(got, "as=root") {
		t.Errorf("formatEvent should show as=root for elevation, got: %q", got)
	}
}

// TestFormatEvent_OriginIncluded covers the privileged-half audit
// improvement: when cmd/sudo.go logs as root, the originating SSH user
// (SUDO_USER) should appear as origin= so an auditor can correlate
// without timestamp matching.
func TestFormatEvent_OriginIncluded(t *testing.T) {
	t.Parallel()
	got := formatEvent("ALLOWED", "root", "root", "tester", "/bin/whoami")
	if !strings.Contains(got, "origin=tester") {
		t.Errorf("formatEvent should include origin=tester, got: %q", got)
	}
}

// TestFormatEvent_OriginOmittedWhenSameAsUser keeps the simple case
// uncluttered: when invoked outside sudo, origin == user and the field
// is dropped.
func TestFormatEvent_OriginOmittedWhenSameAsUser(t *testing.T) {
	t.Parallel()
	got := formatEvent("ALLOWED", "root", "root", "root", "/bin/whoami")
	if strings.Contains(got, "origin=") {
		t.Errorf("formatEvent should omit origin= when equal to user, got: %q", got)
	}
}
