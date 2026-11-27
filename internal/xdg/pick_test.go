package xdg

import "testing"

// pick is the pure per-kind rule; on Linux kit's default equals the
// previous dir, so the full matrix is only reachable here, on every OS.
func TestPick(t *testing.T) {
	for _, tc := range []struct {
		name   string
		envSet bool
		legacy bool
		want   string
	}{
		{"fresh install -> native", false, false, "native"},
		{"legacy install, env unset -> previous", false, true, "prev"},
		{"env set beats legacy", true, true, "native"},
		{"env set, fresh -> native", true, false, "native"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pick("native", "prev", tc.envSet, tc.legacy); got != tc.want {
				t.Fatalf("pick = %q, want %q", got, tc.want)
			}
		})
	}
	if got := pick("native", "", false, true); got != "native" {
		t.Fatalf("empty previous = %q, want native", got)
	}
}
