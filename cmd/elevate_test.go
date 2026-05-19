package cmd

import "testing"

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		allowed   []string
		self      string
		want      string
	}{
		{"self → self when self allowed", "self", []string{"self"}, "ai", "ai"},
		{"self → self when both allowed", "self", []string{"self", "root"}, "ai", "ai"},
		{"root → root when root allowed", "root", []string{"self", "root"}, "ai", "root"},
		{"root denied when only self allowed", "root", []string{"self"}, "ai", ""},
		{"self → root implicit (single non-self target)", "self", []string{"root"}, "ai", "root"},
		{"self denied when ambiguous non-self list", "self", []string{"root", "deploy"}, "ai", ""},
		{"deploy → deploy when listed", "deploy", []string{"self", "deploy"}, "ai", "deploy"},
		{"random user denied", "mallory", []string{"self", "root"}, "ai", ""},
		{"self → self by name match", "ai", []string{"self"}, "ai", "ai"},
	}
	for _, tc := range tests {
		got := resolveTarget(tc.requested, tc.allowed, tc.self)
		if got != tc.want {
			t.Errorf("%s: resolveTarget(%q, %v, %q) = %q, want %q",
				tc.name, tc.requested, tc.allowed, tc.self, got, tc.want)
		}
	}
}
