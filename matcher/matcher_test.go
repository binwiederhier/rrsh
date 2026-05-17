package matcher

import (
	"regexp"
	"testing"

	"github.com/pheckel/noshell/config"
)

func testMatcher() *Matcher {
	return New([]config.CommandRule{
		{Path: "/usr/bin/whoami"},
		{Path: "/usr/bin/ls", ArgsPattern: regexp.MustCompile(`^-la /var/log/.*$`)},
		{Path: "/usr/bin/ps", ArgsPattern: regexp.MustCompile(`^(aux|-ef)$`)},
		{Path: "/usr/bin/df"},
	})
}

func TestMatch_AllowedNoArgs(t *testing.T) {
	rule, ok := testMatcher().Match("/usr/bin/whoami")
	if !ok || rule.Path != "/usr/bin/whoami" {
		t.Error("whoami should be allowed")
	}
}

func TestMatch_AllowedWithArgs(t *testing.T) {
	rule, ok := testMatcher().Match("/usr/bin/ls -la /var/log/syslog")
	if !ok || rule.Path != "/usr/bin/ls" {
		t.Error("ls -la /var/log/syslog should be allowed")
	}
}

func TestMatch_AllowedNoRestrictionWithArgs(t *testing.T) {
	_, ok := testMatcher().Match("/usr/bin/df -h")
	if !ok {
		t.Error("df -h should be allowed (no args restriction)")
	}
}

func TestMatch_DeniedWrongArgs(t *testing.T) {
	_, ok := testMatcher().Match("/usr/bin/ls -la /etc/passwd")
	if ok {
		t.Error("ls -la /etc/passwd should be denied")
	}
}

func TestMatch_DeniedUnknownCommand(t *testing.T) {
	_, ok := testMatcher().Match("/usr/bin/rm -rf /")
	if ok {
		t.Error("rm should be denied")
	}
}

func TestMatch_DeniedRelativePath(t *testing.T) {
	_, ok := testMatcher().Match("whoami")
	if ok {
		t.Error("relative path should be denied")
	}
}

func TestMatch_DeniedMetachars(t *testing.T) {
	m := testMatcher()

	tests := []string{
		"/usr/bin/whoami; rm -rf /",
		"/usr/bin/whoami | cat",
		"/usr/bin/whoami & echo pwned",
		"/usr/bin/whoami $(cat /etc/shadow)",
		"/usr/bin/whoami `cat /etc/shadow`",
		"/usr/bin/whoami > /tmp/out",
		"/usr/bin/whoami < /dev/null",
		"/usr/bin/ls && rm -rf /",
	}

	for _, input := range tests {
		if _, ok := m.Match(input); ok {
			t.Errorf("should deny metachar input: %s", input)
		}
	}
}

func TestMatch_ArgsRegex(t *testing.T) {
	m := testMatcher()

	if _, ok := m.Match("/usr/bin/ps aux"); !ok {
		t.Error("ps aux should be allowed")
	}

	if _, ok := m.Match("/usr/bin/ps -ef"); !ok {
		t.Error("ps -ef should be allowed")
	}

	if _, ok := m.Match("/usr/bin/ps -aux --sort"); ok {
		t.Error("ps -aux --sort should be denied")
	}
}

func TestMatch_EmptyInput(t *testing.T) {
	if _, ok := testMatcher().Match(""); ok {
		t.Error("empty input should not match")
	}
}
