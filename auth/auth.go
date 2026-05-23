package auth

import (
	"errors"
	"slices"

	"github.com/binwiederhier/rrsh/util"
)

// SelfUser is the magic token in a rule's "as:" list meaning "the SSH
// user who invoked rrsh". Spelled "$USER" so it can't collide with a
// real POSIX username (which can't start with "$").
const SelfUser = "$USER"

// ErrNotPermitted is returned by Check when the requested target user
// is not in the rule's allowed list.
var ErrNotPermitted = errors.New("requested user not permitted by rule's as: list")

// Check returns nil if requestedUser is in allowedUsers, else
// ErrNotPermitted. The list must have been run through Resolve so it
// contains no SelfUser sentinels.
func Check(requestedUser string, allowedUsers []string) error {
	if slices.Contains(allowedUsers, requestedUser) {
		return nil
	}
	return ErrNotPermitted
}

// Resolve substitutes SelfUser -> selfUser and deduplicates.
func Resolve(allowedUsers []string, selfUser string) []string {
	substituted := make([]string, len(allowedUsers))
	for i, u := range allowedUsers {
		if u == SelfUser {
			u = selfUser
		}
		substituted[i] = u
	}
	return util.Dedup(substituted)
}
