package config

import (
	"fmt"

	"hop.top/gym/internal/netpolicy"
)

// NetworkConfig is the `network` block: which hosts gym may contact.
type NetworkConfig struct {
	// Mode is open|allowlist|offline.
	Mode string `json:"mode" yaml:"mode"`
	// AllowHosts lists "host", "host:port" or "*.suffix" entries; it is
	// consulted under mode allowlist only.
	AllowHosts []string `json:"allow_hosts" yaml:"allow_hosts"`
}

// Policy is the netpolicy view of the block.
func (n NetworkConfig) Policy() netpolicy.Policy {
	return netpolicy.Policy{Mode: n.Mode, AllowHosts: n.AllowHosts}
}

// validate checks a block; with allowUnset an empty mode means unset.
func (n NetworkConfig) validate(allowUnset bool) error {
	if n.Mode != "" || !allowUnset {
		if _, ok := netpolicy.ModeRank(n.Mode); !ok {
			return fmt.Errorf("invalid network.mode %q (one of: open, allowlist, offline)", n.Mode)
		}
	}
	for _, h := range n.AllowHosts {
		if err := netpolicy.ValidatePattern(h); err != nil {
			return err
		}
	}
	return nil
}

// ClampNetwork folds one folder layer into acc, tighten-only: the mode may
// only rise (open < allowlist < offline) and a layer's allow_hosts only
// intersect with what acc already allows. A layer's unset values (empty
// mode, nil list) leave acc alone. When acc is open with no hosts listed
// there is nothing to intersect with, so the layer's list is taken as is.
func ClampNetwork(acc, layer NetworkConfig) NetworkConfig {
	out := NetworkConfig{Mode: netpolicy.Raise(acc.Mode, layer.Mode), AllowHosts: acc.AllowHosts}
	if layer.AllowHosts == nil {
		return out
	}
	if len(acc.AllowHosts) == 0 && (acc.Mode == "" || acc.Mode == netpolicy.ModeOpen) {
		out.AllowHosts = append([]string{}, layer.AllowHosts...)
		return out
	}
	out.AllowHosts = netpolicy.Intersect(acc.AllowHosts, layer.AllowHosts)
	return out
}

// foldNetworkLayer reads the `network` block of the folder layer at path
// and folds it into acc.
func foldNetworkLayer(acc NetworkConfig, path string) (NetworkConfig, error) {
	keys, err := readProjectLayer(path)
	if err != nil {
		return acc, err
	}
	v, ok := keys["network"]
	if !ok {
		return acc, nil
	}
	var layer NetworkConfig
	if err := decodeKey(v, &layer); err != nil {
		return acc, fmt.Errorf("load config %s: network: %w", path, err)
	}
	if err := layer.validate(true); err != nil {
		return acc, fmt.Errorf("%s: %w", path, err)
	}
	return ClampNetwork(acc, layer), nil
}
