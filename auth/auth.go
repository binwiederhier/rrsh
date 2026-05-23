package auth

import (
	"errors"
	"slices"

	"github.com/binwiederhier/rrsh/util"
)

// SelfUser is the magic token in a rule's "as:" list meaning "the SSH
// user who invoked rrsh"
const SelfUser = "self"

// ErrNotPermitted is returned by Check when the requested target user
// is not in the rule's allowed list.
var ErrNotPermitted = errors.New("requested user not permitted by rule's as: list")

// Check returns nil if requestedUser is in allowedUsers, or
// ErrNotPermitted otherwise. The list must already have been processed
// through Resolve so it contains no SelfUser sentinel values.
func Check(requestedUser string, allowedUsers []string) error {
	if slices.Contains(allowedUsers, requestedUser) {
		return nil
	}
	return ErrNotPermitted
}

// Resolve substitutes any SelfUser entries in allowedUsers with the
// concrete selfUser (the originating SSH identity) and deduplicates.
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
