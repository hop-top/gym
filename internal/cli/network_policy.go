package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	kitcli "hop.top/kit/go/console/cli"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/netpolicy"
)

// applyNetwork layers the global --offline flag over the configured network
// policy (offline raises the mode, never lowers it) and installs the result
// as the process-wide policy every egress consults.
func (a *App) applyNetwork(cfg *config.Config) {
	if a.offline {
		cfg.Network.Mode = netpolicy.Raise(cfg.Network.Mode, netpolicy.ModeOffline)
	}
	netpolicy.Set(cfg.Network.Policy())
}

// initNetworkPolicy installs the policy for the command about to run,
// before anything can reach the network (event bus, notifier, judge). A
// configuration that cannot be read denies the network for the run: the
// command fails on the same error when it loads its runtime.
func (a *App) initNetworkPolicy(c *cobra.Command) {
	a.offline, _ = c.Flags().GetBool("offline")
	// Kit's own guard under http.DefaultTransport refuses requests on an
	// offline-marked context; gym replaces kit's PersistentPreRunE, so the
	// marker kit's hook would stamp is stamped here.
	defer func() {
		c.SetContext(kitcli.WithOffline(c.Context(), netpolicy.Current().Mode == netpolicy.ModeOffline))
	}()
	cfg, err := config.LoadMergedConfig(a.startDir(), a.cfgPaths, a.cfgOverrides)
	if err != nil {
		netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeOffline})
		return
	}
	a.applyNetwork(&cfg)
}

// judgeNetworkGate lets the judge shell out to rsx only when the network
// is open: rsx reads from public hosts.
func judgeNetworkGate() error {
	if netpolicy.Open() {
		return nil
	}
	return fmt.Errorf("network.mode is %s", netpolicy.Current().Mode)
}
