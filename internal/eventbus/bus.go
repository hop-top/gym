package eventbus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/netpolicy"
)

// Adapter selects the underlying transport.
type Adapter string

const (
	// AdapterMemory is the in-process default; fast and ephemeral.
	AdapterMemory Adapter = "memory"
	// AdapterSQLite persists events to $XDG_STATE_HOME/gym/bus.sqlite
	// (or GYM_BUS_PATH) and polls for delivery. Cross-process safe.
	AdapterSQLite Adapter = "sqlite"
	// AdapterNetwork bridges this bus to GYM_BUS_URL via WebSocket.
	AdapterNetwork Adapter = "network"
)

// Config configures Bus construction. Zero value is valid: it produces
// an in-memory bus with the default Source/OriginID derived from
// kit/identity (or hostname fallback).
type Config struct {
	// Adapter is the explicit adapter selection. When empty, Resolve
	// inspects environment + flags and picks one.
	Adapter Adapter

	// SQLitePath overrides the default $XDG_STATE_HOME/gym/bus.sqlite
	// path when Adapter is AdapterSQLite. Honored before
	// GYM_BUS_PATH.
	SQLitePath string

	// NetworkURL is the WebSocket address joined when Adapter is
	// AdapterNetwork.
	NetworkURL string

	// Source overrides the default Source ("gym").
	Source string

	// OriginID overrides the kit/identity-derived origin ID.
	OriginID string
}

// Bus is gym's bus handle. It bundles the underlying kit bus, the
// resolved Source string, and an OriginID stable across the lifetime
// of the process.
type Bus struct {
	bus      bus.Bus
	source   string
	originID string
	adapter  Adapter
}

// New constructs a Bus per the supplied Config. Adapter selection is
// applied via Resolve when cfg.Adapter is empty.
//
// The returned Bus owns its underlying transport — call Close to
// release SQLite handles or WebSocket connections.
func New(cfg Config) (*Bus, error) {
	resolved := Resolve(cfg)

	source := cfg.Source
	if source == "" {
		source = Source
	}
	origin := cfg.OriginID
	if origin == "" {
		origin = originID()
	}

	var (
		b   bus.Bus
		err error
	)
	switch resolved.Adapter {
	case AdapterMemory:
		b = bus.New()
	case AdapterSQLite:
		path := resolved.SQLitePath
		if path == "" {
			path, err = defaultSQLitePath()
			if err != nil {
				return nil, fmt.Errorf("eventbus: resolve sqlite path: %w", err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("eventbus: create sqlite parent: %w", err)
		}
		ad, err := bus.NewSQLiteAdapter(path)
		if err != nil {
			return nil, fmt.Errorf("eventbus: open sqlite adapter: %w", err)
		}
		b = bus.New(bus.WithAdapter(ad))
	case AdapterNetwork:
		if resolved.NetworkURL == "" {
			return nil, errors.New("eventbus: network adapter requires GYM_BUS_URL")
		}
		// The adapter dials a remote hub: an egress like any other.
		if err := netpolicy.Check(resolved.NetworkURL); err != nil {
			return nil, err
		}
		// Allow-list: gym.* + curated peers gym subscribes to.
		filter := bus.TopicFilter{
			Allow: []string{
				"gym.#",
				"rsx.repo.#",
				"policy.guard.#",
				"tip.shell.#",
				"toolspec.cli.#",
			},
		}
		b = bus.New(
			bus.WithNetwork(resolved.NetworkURL),
			bus.WithNetworkOption(
				bus.WithFilter(filter),
				bus.WithOriginID(origin),
			),
		)
	default:
		return nil, fmt.Errorf("eventbus: unknown adapter %q", resolved.Adapter)
	}

	return &Bus{
		bus:      b,
		source:   source,
		originID: origin,
		adapter:  resolved.Adapter,
	}, nil
}

// Resolve applies environment-based fallbacks to cfg and returns the
// effective configuration. It does NOT open any handles — pure
// resolution, safe to call from tests.
//
// Precedence for adapter:
//  1. cfg.Adapter (explicit)
//  2. GYM_BUS_URL set -> Network
//  3. GYM_BUS_PATH set -> SQLite
//  4. AdapterMemory (default)
func Resolve(cfg Config) Config {
	out := cfg
	if out.Source == "" {
		out.Source = Source
	}
	if out.NetworkURL == "" {
		out.NetworkURL = strings.TrimSpace(os.Getenv("GYM_BUS_URL"))
	}
	if out.SQLitePath == "" {
		out.SQLitePath = strings.TrimSpace(os.Getenv("GYM_BUS_PATH"))
	}
	if out.Adapter == "" {
		switch {
		case out.NetworkURL != "":
			out.Adapter = AdapterNetwork
		case out.SQLitePath != "":
			out.Adapter = AdapterSQLite
		default:
			out.Adapter = AdapterMemory
		}
	}
	return out
}

// defaultSQLitePath returns $XDG_STATE_HOME/gym/bus.sqlite. Uses the
// raw path so an unwritable XDG guard config still produces a usable
// fallback on machines where state isn't expected to persist.
func defaultSQLitePath() (string, error) {
	state, err := xdg.StateDir("gym")
	if err == nil {
		return filepath.Join(state, "bus.sqlite"), nil
	}
	raw, rawErr := xdg.RawStateDir("gym")
	if rawErr != nil {
		return "", errors.Join(err, rawErr)
	}
	return filepath.Join(raw, "bus.sqlite"), nil
}

// Underlying returns the kit bus.Bus for advanced callers (subscribe
// patterns the wrapper doesn't expose). Most call sites should prefer
// Subscribe / Publish on the wrapper itself.
func (b *Bus) Underlying() bus.Bus { return b.bus }

// Source returns the configured source string (always "gym" unless
// overridden).
func (b *Bus) Source() string { return b.source }

// OriginID returns the stable origin ID for this binary instance.
func (b *Bus) OriginID() string { return b.originID }

// AdapterKind returns the resolved adapter kind for diagnostics.
func (b *Bus) AdapterKind() Adapter { return b.adapter }

// Publish wraps bus.Publish, populating Source automatically. Topics
// must conform to kit/bus's 4-segment past-tense convention.
func (b *Bus) Publish(ctx context.Context, topic bus.Topic, payload any) error {
	if b == nil || b.bus == nil {
		return errors.New("eventbus: nil bus")
	}
	return b.bus.Publish(ctx, bus.NewEvent(topic, b.source, payload))
}

// Subscribe registers a sync handler on pattern. Returning an error
// from h vetoes the publish for all later sync subscribers (kit/bus
// contract).
func (b *Bus) Subscribe(pattern string, h bus.Handler) bus.Unsubscribe {
	return b.bus.Subscribe(pattern, h)
}

// SubscribeAsync registers an async handler. Async handlers run in a
// bounded goroutine pool and never block Publish.
func (b *Bus) SubscribeAsync(pattern string, h bus.AsyncHandler) bus.Unsubscribe {
	return b.bus.SubscribeAsync(pattern, h)
}

// Close shuts the bus down, draining in-flight async handlers up to
// the supplied context deadline.
func (b *Bus) Close(ctx context.Context) error {
	if b == nil || b.bus == nil {
		return nil
	}
	return b.bus.Close(ctx)
}
