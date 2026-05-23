package util

import (
	"sort"
	"testing"
)

func TestDedup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"single", []string{"a"}, []string{"a"}},
		{"all distinct", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"all duplicate", []string{"a", "a", "a"}, []string{"a"}},
		{"mixed", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Dedup(tc.in)
			// Output order is unspecified; compare as sets via sort.
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !slicesEqual(got, want) {
				t.Errorf("Dedup(%v) = %v, want %v (as a set)", tc.in, got, tc.want)
			}
		})
	}
}

// TestDedup_Generic exercises the type parameter with an int slice.
func TestDedup_Generic(t *testing.T) {
	t.Parallel()
	got := Dedup([]int{1, 2, 1, 3, 2})
	sort.Ints(got)
	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("Dedup([]int) length = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
