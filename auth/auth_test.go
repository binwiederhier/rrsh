package auth

import (
	"errors"
	"testing"
)

func TestCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		requested string
		self      string
		allowed   []string
		want      error
	}{
		{"self matches via sentinel", "tester", "tester", []string{SelfUser}, nil},
		{"self does not match when current user is not allowed", "root", "tester", []string{SelfUser}, ErrNotPermitted},
		{"explicit user matches", "root", "tester", []string{"root"}, nil},
		{"explicit user not listed", "deploy", "tester", []string{"root"}, ErrNotPermitted},
		{"empty allowed list denies", "tester", "tester", nil, ErrNotPermitted},
		{"mixed list matches non-self entry", "root", "tester", []string{SelfUser, "root"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Check(tc.requested, tc.self, tc.allowed)
			if !errors.Is(got, tc.want) {
				t.Errorf("Check(%q, %q, %v) = %v, want %v", tc.requested, tc.self, tc.allowed, got, tc.want)
			}
		})
	}
}
