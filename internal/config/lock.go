package config

// LockConfig tunes how commands treat the lockfile.
type LockConfig struct {
	// Frozen makes learn and train refuse any change to what the lock
	// records (lock-frozen). A project or folder layer may turn it on and
	// never off; user, system and -c layers may set it either way.
	Frozen bool `json:"frozen" yaml:"frozen"`
}
