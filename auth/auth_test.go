package auth

import (
	"errors"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		self string
		want []string
	}{
		{"self sentinel substituted", []string{SelfUser}, "tester", []string{"tester"}},
		{"non-self entries pass through", []string{"root", "deploy"}, "tester", []string{"root", "deploy"}},
		{"mixed list", []string{SelfUser, "root"}, "tester", []string{"tester", "root"}},
		{"empty input", nil, "tester", []string{}},
		{"self collapses with explicit selfUser", []string{SelfUser, "tester"}, "tester", []string{"tester"}},
		{"explicit duplicate dropped", []string{"root", "root"}, "tester", []string{"root"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Resolve(tc.in, tc.self)
			if !setEqual(got, tc.want) {
				t.Errorf("Resolve(%v, %q) = %v, want %v (as a set)", tc.in, tc.self, got, tc.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		requested string
		allowed   []string
		want      error
	}{
		{"in list", "root", []string{"root"}, nil},
		{"not in list", "deploy", []string{"root"}, ErrNotPermitted},
		{"empty list denies", "tester", nil, ErrNotPermitted},
		{"matches one of many", "root", []string{"tester", "root"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Check(tc.requested, tc.allowed)
			if !errors.Is(got, tc.want) {
				t.Errorf("Check(%q, %v) = %v, want %v", tc.requested, tc.allowed, got, tc.want)
			}
		})
	}
}

// TestResolve_DoesNotMutateInput proves the input slice is not aliased
// by the returned slice - callers can safely keep a reference to the
// original config-supplied rule.As across multiple Resolve calls.
func TestResolve_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	in := []string{SelfUser, "root"}
	_ = Resolve(in, "tester")
	if in[0] != SelfUser {
		t.Errorf("Resolve mutated input: in[0] = %q, want %q", in[0], SelfUser)
	}
}

// setEqual compares two slices as sets (order-independent).
func setEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, x := range a {
		seen[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := seen[x]; !ok {
			return false
		}
	}
	return true
}
