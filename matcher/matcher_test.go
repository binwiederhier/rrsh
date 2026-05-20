package matcher

import (
	"regexp"
	"testing"

	"github.com/binwiederhier/rrsh/config"
)

func testMatcher(t *testing.T) *Matcher {
	t.Helper()
	return New([]config.CommandRule{
		{Path: "/usr/bin/whoami"},
		{Path: "/usr/bin/ls", ArgsPattern: regexp.MustCompile(`^-la /var/log/.*$`)},
		{Path: "/usr/bin/ps", ArgsPattern: regexp.MustCompile(`^(aux|-ef)$`)},
		{Path: "/usr/bin/df"},
		{Path: "/usr/bin/grep"},
	})
}

func TestMatch_AllowedNoArgs(t *testing.T) {
	t.Parallel()
	rule, ok := testMatcher(t).Match("/usr/bin/whoami", nil)
	if !ok || rule.Path != "/usr/bin/whoami" {
		t.Error("whoami should be allowed")
	}
}

func TestMatch_AllowedWithArgs(t *testing.T) {
	t.Parallel()
	rule, ok := testMatcher(t).Match("/usr/bin/ls", []string{"-la", "/var/log/syslog"})
	if !ok || rule.Path != "/usr/bin/ls" {
		t.Error("ls -la /var/log/syslog should be allowed")
	}
}

func TestMatch_AllowedNoRestrictionWithArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/df", []string{"-h"})
	if !ok {
		t.Error("df -h should be allowed (no args restriction)")
	}
}

func TestMatch_DeniedWrongArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/ls", []string{"-la", "/etc/passwd"})
	if ok {
		t.Error("ls -la /etc/passwd should be denied")
	}
}

func TestMatch_DeniedUnknownCommand(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/rm", []string{"-rf", "/"})
	if ok {
		t.Error("rm should be denied")
	}
}

func TestMatch_DeniedRelativePath(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("whoami", nil)
	if ok {
		t.Error("relative path should be denied")
	}
}

// Metacharacters in argv element values are no longer a parser concern:
// argv arrives as a slice, so a "|" or ";" inside an element is just a
// byte. Only the rule's regex decides whether the value is allowed.
func TestMatch_MetacharsInArgvElementAreLiteralBytes(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	// /usr/bin/grep has no ArgsPattern, so any single string arg is allowed
	// including one containing pipe / redirect characters.
	if _, ok := m.Match("/usr/bin/grep", []string{" | > /dev/null"}); !ok {
		t.Error("grep with quoted metachar arg should be allowed (no args regex)")
	}
}

func TestMatch_ArgsRegex(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	if _, ok := m.Match("/usr/bin/ps", []string{"aux"}); !ok {
		t.Error("ps aux should be allowed")
	}

	if _, ok := m.Match("/usr/bin/ps", []string{"-ef"}); !ok {
		t.Error("ps -ef should be allowed")
	}

	if _, ok := m.Match("/usr/bin/ps", []string{"-aux", "--sort"}); ok {
		t.Error("ps -aux --sort should be denied")
	}
}

func TestMatch_EmptyPath(t *testing.T) {
	t.Parallel()
	if _, ok := testMatcher(t).Match("", nil); ok {
		t.Error("empty path should not match")
	}
}
