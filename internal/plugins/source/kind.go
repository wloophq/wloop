package source

import (
	"context"
	"fmt"
	"sync"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Kind starts and stops the source kind of plugin processes for the
// plugin host, and holds the client of each running one.
type Kind struct {
	// Broker is the broker the host serves plugin id, whose source table
	// is s.
	Broker func(id string, s *manifest.Source) *Broker
	// Snapshots are marked stale while a plugin is down.
	Snapshots *Snapshots
	// StateDir is plugin id's own state directory.
	StateDir func(id string) string

	mu      sync.Mutex
	running map[string]*attached
}

type attached struct {
	client *src.Client
	server *src.HostServer
}

// Kind is the source kind.
func (k *Kind) Kind() plugin.Kind { return src.Kind }

// Start serves the broker to plugin m's freshly started process and starts
// its source kind; until it returns, the plugin's units stay stale.
func (k *Kind) Start(ctx context.Context, m *manifest.Manifest, c *plugin.Client) error {
	s, err := m.Source()
	if err != nil {
		return err
	}
	server, err := src.ServeHost(c, k.Broker(m.ID, s).Host())
	if err != nil {
		return err
	}
	if err := c.Start(ctx, plugin.StartRequest{StateDir: k.StateDir(m.ID), HostBroker: server.ID()}); err != nil {
		server.Close()
		return fmt.Errorf("start %s: %w", m.ID, err)
	}
	k.mu.Lock()
	if k.running == nil {
		k.running = map[string]*attached{}
	}
	k.running[m.ID] = &attached{client: src.NewClient(c), server: server}
	k.mu.Unlock()
	k.Snapshots.SetDown(unit.ProviderID(m.ID), false)
	return nil
}

// Stop asks plugin id's source kind to stop and stops serving its broker.
func (k *Kind) Stop(ctx context.Context, id string) {
	a := k.detach(id)
	if a == nil {
		return
	}
	_ = a.client.Stop(ctx)
	a.server.Close()
}

// Down marks plugin id's units stale: its process ended.
func (k *Kind) Down(id string) {
	if a := k.detach(id); a != nil {
		a.server.Close()
	}
	k.Snapshots.SetDown(unit.ProviderID(id), true)
}

func (k *Kind) detach(id string) *attached {
	k.mu.Lock()
	defer k.mu.Unlock()
	a := k.running[id]
	delete(k.running, id)
	return a
}

// Client is the client of plugin id's running source kind; ErrUnavailable
// while it does not run.
func (k *Kind) Client(id string) (*src.Client, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if a := k.running[id]; a != nil {
		return a.client, nil
	}
	return nil, fmt.Errorf("plugin %s: %w", id, src.ErrUnavailable)
}
