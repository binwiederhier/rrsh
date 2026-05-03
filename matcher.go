package main

import (
	"strings"
)

// shellMetachars are characters that indicate shell injection attempts.
const shellMetachars = "|;&$`>()<"

// Match checks if the input command is allowed by any rule.
// It returns whether a match was found and the matching rule.
func Match(rules []CommandRule, input string) (bool, *CommandRule) {
	if containsMetachars(input) {
		return false, nil
	}

	cmd, args := splitCommand(input)

	// Reject non-absolute paths
	if !strings.HasPrefix(cmd, "/") {
		return false, nil
	}

	for i := range rules {
		if rules[i].Path != cmd {
			continue
		}
		if rules[i].ArgsPattern == nil {
			return true, &rules[i]
		}
		if rules[i].ArgsPattern.MatchString(args) {
			return true, &rules[i]
		}
		return false, nil
	}

	return false, nil
}

// splitCommand splits input into the command and the remaining args string.
func splitCommand(input string) (string, string) {
	input = strings.TrimSpace(input)
	idx := strings.IndexByte(input, ' ')
	if idx == -1 {
		return input, ""
	}
	return input[:idx], input[idx+1:]
}

// containsMetachars returns true if input contains any shell metacharacters.
func containsMetachars(input string) bool {
	return strings.ContainsAny(input, shellMetachars)
}
