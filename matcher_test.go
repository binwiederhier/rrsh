package main

import (
	"regexp"
	"testing"
)

func testRules() []CommandRule {
	return []CommandRule{
		{Path: "/usr/bin/whoami"},
		{Path: "/usr/bin/ls", ArgsPattern: regexp.MustCompile(`^-la /var/log/.*$`)},
		{Path: "/usr/bin/ps", ArgsPattern: regexp.MustCompile(`^(aux|-ef)$`)},
		{Path: "/usr/bin/df"},
	}
}

func TestMatch_AllowedNoArgs(t *testing.T) {
	rules := testRules()
	matched, rule := Match(rules, "/usr/bin/whoami")
	if !matched || rule.Path != "/usr/bin/whoami" {
		t.Error("whoami should be allowed")
	}
}

func TestMatch_AllowedWithArgs(t *testing.T) {
	rules := testRules()
	matched, rule := Match(rules, "/usr/bin/ls -la /var/log/syslog")
	if !matched || rule.Path != "/usr/bin/ls" {
		t.Error("ls -la /var/log/syslog should be allowed")
	}
}

func TestMatch_AllowedNoRestrictionWithArgs(t *testing.T) {
	rules := testRules()
	matched, _ := Match(rules, "/usr/bin/df -h")
	if !matched {
		t.Error("df -h should be allowed (no args restriction)")
	}
}

func TestMatch_DeniedWrongArgs(t *testing.T) {
	rules := testRules()
	matched, _ := Match(rules, "/usr/bin/ls -la /etc/passwd")
	if matched {
		t.Error("ls -la /etc/passwd should be denied")
	}
}

func TestMatch_DeniedUnknownCommand(t *testing.T) {
	rules := testRules()
	matched, _ := Match(rules, "/usr/bin/rm -rf /")
	if matched {
		t.Error("rm should be denied")
	}
}

func TestMatch_DeniedRelativePath(t *testing.T) {
	rules := testRules()
	matched, _ := Match(rules, "whoami")
	if matched {
		t.Error("relative path should be denied")
	}
}

func TestMatch_DeniedMetachars(t *testing.T) {
	rules := testRules()

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
		matched, _ := Match(rules, input)
		if matched {
			t.Errorf("should deny metachar input: %s", input)
		}
	}
}

func TestMatch_ArgsRegex(t *testing.T) {
	rules := testRules()

	matched, _ := Match(rules, "/usr/bin/ps aux")
	if !matched {
		t.Error("ps aux should be allowed")
	}

	matched, _ = Match(rules, "/usr/bin/ps -ef")
	if !matched {
		t.Error("ps -ef should be allowed")
	}

	matched, _ = Match(rules, "/usr/bin/ps -aux --sort")
	if matched {
		t.Error("ps -aux --sort should be denied")
	}
}

func TestMatch_EmptyInput(t *testing.T) {
	rules := testRules()
	matched, _ := Match(rules, "")
	if matched {
		t.Error("empty input should not match")
	}
}
