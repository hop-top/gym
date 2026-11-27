package eventbus

import (
	"github.com/spf13/viper"
)

// StrictKey is the viper key for the global --strict flag. Exported so
// command wiring (cobra.go) and consumers (judge/guard) bind to the
// same name.
const StrictKey = "strict"

// IsStrict reports whether strict mode is in effect. Strict mode flips
// timeout and warning verdicts to fail-closed for sync gates.
//
// Resolution order (first non-zero wins):
//  1. vp.GetBool(StrictKey) (set by --strict flag binding)
//  2. vp.GetBool("gym.strict") (config-file form)
//  3. defaults to false
//
// A nil viper falls through to the global instance so callers without
// per-command viper plumbing still get the right answer.
func IsStrict(vp *viper.Viper) bool {
	if vp == nil {
		vp = viper.GetViper()
	}
	if vp == nil {
		return false
	}
	if vp.IsSet(StrictKey) {
		return vp.GetBool(StrictKey)
	}
	if vp.IsSet("gym.strict") {
		return vp.GetBool("gym.strict")
	}
	return false
}
