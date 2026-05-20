package cmd

import (
	"reflect"
	"testing"
)

func TestResolveAllowedUsers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		allowed []string
		self    string
		want    []string
	}{
		{"empty list", nil, "ai", []string{}},
		{"self resolves to current user", []string{"self"}, "ai", []string{"ai"}},
		{"plain users pass through", []string{"root", "deploy"}, "ai", []string{"root", "deploy"}},
		{"mixed list", []string{"self", "root"}, "ai", []string{"ai", "root"}},
	}
	for _, tc := range tests {
		got := resolveAllowedUsers(tc.allowed, tc.self)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: resolveAllowedUsers(%v, %q) = %v, want %v",
				tc.name, tc.allowed, tc.self, got, tc.want)
		}
	}
}
