package eventbus

import (
	"errors"
	"fmt"
	"os"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/core/xdg"
)

// Source is what gym sets on bus.Event.Source. Always "gym" for events
// originated by this binary.
const Source = "gym"

// originID returns a stable per-binary identifier suitable for
// bus.Event correlation. It tries kit/core/identity first; on any
// failure (XDG not writable, identity disabled, etc.) it falls back to
// "gym-<hostname>".
//
// The function never returns an error — identity must not be a
// crash-vector for the CLI. Callers can override the resolved value at
// boot via WithOriginID.
func originID() string {
	if id, ok := identityFingerprint(); ok {
		return "gym-" + id
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return "gym-" + host
}

// identityFingerprint loads (or generates) the kit identity keypair
// and returns its short public-key fingerprint. The bool is false when
// identity is unavailable for any reason — the caller should fall back.
func identityFingerprint() (string, bool) {
	store, err := defaultIdentityStore()
	if err != nil {
		return "", false
	}
	kp, err := store.LoadOrGenerate()
	if err != nil {
		return "", false
	}
	id := kp.PublicKeyID()
	if id == "" {
		return "", false
	}
	return id, true
}

// defaultIdentityStore wraps identity.DefaultStore() so we can reason
// about the failure mode (sandboxed XDG, read-only home, etc.) without
// surfacing the kit's specific error message to the user.
func defaultIdentityStore() (*identity.Store, error) {
	store, err := identity.DefaultStore()
	if err == nil {
		return store, nil
	}
	// Best-effort: if the data dir resolution failed under the kit
	// XDG guard, try the raw path so we still get a stable identity
	// when running outside an interactive shell (e.g. CI sandbox).
	raw, rawErr := xdg.RawDataDir("kit")
	if rawErr != nil {
		return nil, fmt.Errorf("identity: %w", errors.Join(err, rawErr))
	}
	return identity.NewStore(raw + "/identity")
}
