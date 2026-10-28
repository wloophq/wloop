package source

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// DefaultHealthPoll is how often a Kind with a zero HealthPoll asks a
// loading plugin its health.
const DefaultHealthPoll = time.Second

// DefaultNotifyTimeout is the bound of one Notify of a Kind with a zero
// NotifyTimeout.
const DefaultNotifyTimeout = 10 * time.Second

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
	// Loaded is called once a plugin expected to load first reports it has
	// imported its data and reported every unit; nil calls nothing.
	Loaded func(id string)
	// HealthPoll is how often a loading plugin is asked its health.
	HealthPoll time.Duration
	// NotifyTimeout bounds one plugin's Notify, so a plugin that hangs
	// delays the others by no more; zero is DefaultNotifyTimeout.
	NotifyTimeout time.Duration
	// Logf logs a failed notification; nil logs nothing.
	Logf func(format string, args ...any)
	// Meters carry the host's meter scopes to the plugins' calls; nil
	// meters nothing.
	Meters *Meters

	mu      sync.Mutex
	running map[string]*attached
	// loading are the plugins expected to load that have not yet, with why.
	loading map[string]string
}

type attached struct {
	client *src.Client
	// notified is what Notify reaches: the client.
	notified notifier
	server   *src.HostServer
	// notify are the topics the plugin's manifest subscribes to.
	notify []plugin.Topic
	cancel context.CancelFunc
	done   chan struct{}
}

// Reason why an expected plugin has not loaded, before it says why itself.
const (
	ReasonStarting = "starting"
	ReasonDown     = "not running"
)

// Kind is the source kind.
func (k *Kind) Kind() plugin.Kind { return src.Kind }

// Expect says plugins ids are to load on this start: each is loading until
// its health first says it serves.
func (k *Kind) Expect(ids ...string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.loading == nil {
		k.loading = map[string]string{}
	}
	for _, id := range ids {
		k.loading[id] = ReasonStarting
	}
}

// Loading are the expected plugins that have not loaded yet, with why, by id.
func (k *Kind) Loading() map[string]string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make(map[string]string, len(k.loading))
	for id, why := range k.loading {
		out[id] = why
	}
	return out
}

// Start serves the broker to plugin m's freshly started process and starts
// its source kind; until it returns, the plugin's units stay stale. A
// plugin still loading is asked its health until it serves.
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
	a := &attached{client: src.NewClient(c), server: server, done: make(chan struct{})}
	a.notified = a.client
	for _, n := range s.Notify {
		a.notify = append(a.notify, plugin.Topic(n))
	}
	hctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.cancel = cancel
	k.mu.Lock()
	if k.running == nil {
		k.running = map[string]*attached{}
	}
	k.running[m.ID] = a
	_, loading := k.loading[m.ID]
	k.mu.Unlock()
	if loading {
		go func() {
			defer close(a.done)
			k.watchHealth(hctx, m.ID, a.client)
		}()
	} else {
		close(a.done)
	}
	k.Snapshots.SetDown(unit.ProviderID(m.ID), false)
	return nil
}

// watchHealth asks plugin id its health until it serves or ctx ends, and
// keeps why it does not serve yet.
func (k *Kind) watchHealth(ctx context.Context, id string, c *src.Client) {
	poll := k.HealthPoll
	if poll <= 0 {
		poll = DefaultHealthPoll
	}
	for {
		h, err := c.Health(ctx)
		switch {
		case err != nil:
			k.setLoading(id, err.Error())
		case h.Status == plugin.StatusServing || h.Status == plugin.StatusDegraded:
			k.mu.Lock()
			delete(k.loading, id)
			k.mu.Unlock()
			if k.Loaded != nil {
				k.Loaded(id)
			}
			return
		default:
			k.setLoading(id, h.Reason)
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (k *Kind) setLoading(id, why string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.loading[id]; ok {
		k.loading[id] = why
	}
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
	k.setLoading(id, ReasonDown)
	k.Snapshots.SetDown(unit.ProviderID(id), true)
}

// detach forgets plugin id's process and waits for its health watch.
func (k *Kind) detach(id string) *attached {
	k.mu.Lock()
	a := k.running[id]
	delete(k.running, id)
	k.mu.Unlock()
	if a != nil {
		a.cancel()
		<-a.done
	}
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

// subscribers are the running plugins whose manifest subscribes to a topic
// of ts, each with those topics, sorted by id.
func (k *Kind) subscribers(ts []plugin.Topic) []subscriber {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []subscriber
	for id, a := range k.running {
		var topics []plugin.Topic
		for _, t := range ts {
			if slices.Contains(a.notify, t) {
				topics = append(topics, t)
			}
		}
		if len(topics) > 0 {
			out = append(out, subscriber{id: id, client: a.notified, topics: topics})
		}
	}
	slices.SortFunc(out, func(a, b subscriber) int { return cmp.Compare(a.id, b.id) })
	return out
}

type subscriber struct {
	id     string
	client notifier
	topics []plugin.Topic
}

type notifier interface {
	Notify(ctx context.Context, units []plugin.Unit, topics []plugin.Topic) error
}
