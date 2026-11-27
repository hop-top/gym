package xdg

import "testing"

// pick is the pure stickiness rule; on Linux kit's default equals the
// previous dir, so the full matrix is only reachable here, on every OS.
func TestPick(t *testing.T) {
	tmp := t.TempDir()
	missing := tmp + "/missing"
	for _, tc := range []struct {
		name         string
		native, prev string
		envSet       bool
		want         string
	}{
		{"native missing, prev present, env unset -> prev", missing + "-n", tmp, false, tmp},
		{"native present beats prev", tmp, t.TempDir(), false, tmp},
		{"neither -> native", missing + "-n", missing + "-p", false, missing + "-n"},
		{"env set beats prev", missing + "-n", tmp, true, missing + "-n"},
		{"env set, native present", tmp, t.TempDir(), true, tmp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pick(tc.native, tc.prev, tc.envSet); got != tc.want {
				t.Fatalf("pick = %q, want %q", got, tc.want)
			}
		})
	}
}
