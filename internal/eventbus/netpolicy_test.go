package eventbus_test

import (
	"context"
	"errors"
	"testing"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/netpolicy"
)

// The network adapter dials a remote hub: a denied host is a hard
// network-denied error when the bus is built, before any command work.
func TestNetworkAdapterHonoursPolicy(t *testing.T) {
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	cfg := eventbus.Config{Adapter: eventbus.AdapterNetwork, NetworkURL: "ws://hub.corp.example:7000/bus"}

	t.Run("allowlist denies an unlisted hub", func(t *testing.T) {
		defer netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeAllowlist, AllowHosts: []string{"other.example"}})()
		_, err := eventbus.New(cfg)
		var ne *netpolicy.Error
		if !errors.As(err, &ne) || err.Error() != `network-denied: host "hub.corp.example" not in network.allow_hosts (mode allowlist)` {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("offline denies it", func(t *testing.T) {
		defer netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeOffline, AllowHosts: []string{"hub.corp.example"}})()
		_, err := eventbus.New(cfg)
		var ne *netpolicy.Error
		if !errors.As(err, &ne) || err.Error() != `network-denied: offline mode forbids network access to "hub.corp.example"` {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a listed hub passes the policy", func(t *testing.T) {
		defer netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeAllowlist, AllowHosts: []string{"127.0.0.1:1"}})()
		// loopback port 1: nothing listens; only the policy decision matters.
		b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterNetwork, NetworkURL: "ws://127.0.0.1:1/bus"})
		var ne *netpolicy.Error
		if errors.As(err, &ne) {
			t.Fatalf("listed hub denied: %v", err)
		}
		if b != nil {
			_ = b.Close(context.Background())
		}
	})
	t.Run("the memory adapter needs no host", func(t *testing.T) {
		defer netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeOffline})()
		b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
		if err != nil {
			t.Fatal(err)
		}
		_ = b.Close(context.Background())
	})
}
