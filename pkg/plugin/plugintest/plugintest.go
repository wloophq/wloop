// Package plugintest is the in-process fake of a plugin process, for tests on
// either side of the protocol. Its [Launcher] serves the kinds a test
// registers over a real go-plugin gRPC connection inside the test binary.
package plugintest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/wloophq/wloop/pkg/plugin"
)

// Launcher is a [plugin.Launcher] that serves registered kinds in-process.
// Everything it starts is stopped when the test ends.
type Launcher struct {
	t        testing.TB
	mu       sync.Mutex
	impls    map[string]map[plugin.Kind]plugin.Impl
	launches map[string]int
	running  map[string]*process
}

var _ plugin.Launcher = (*Launcher)(nil)

// NewLauncher returns a Launcher with no plugin registered.
func NewLauncher(t testing.TB) *Launcher {
	return &Launcher{t: t, impls: map[string]map[plugin.Kind]plugin.Impl{}, launches: map[string]int{}, running: map[string]*process{}}
}

// Add registers the kinds the plugin with id serves; a later Launch of a
// Spec with that ID serves them.
func (l *Launcher) Add(id string, impls map[plugin.Kind]plugin.Impl) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.impls[id] = impls
}

// Launch connects to the kinds registered for spec.ID, ignoring spec.Path
// and spec.Checksum. A kind spec names and the plugin does not serve is
// dispensed and answers every call with an error, as a real plugin's does.
func (l *Launcher) Launch(_ context.Context, spec plugin.Spec) (plugin.Process, error) {
	l.mu.Lock()
	impls, ok := l.impls[spec.ID]
	l.launches[spec.ID]++
	l.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("plugintest: no plugin %q", spec.ID)
	}
	served := map[plugin.Kind]plugin.Impl{}
	for _, k := range spec.Kinds {
		served[k] = nil
	}
	for k, impl := range impls {
		served[k] = impl
	}
	client, _ := goplugin.TestPluginGRPCConn(l.t, false, plugin.VersionedPlugins(served)[plugin.Protocol])
	p := &process{client: client, kinds: spec.Kinds}
	l.t.Cleanup(p.Kill)
	l.mu.Lock()
	l.running[spec.ID] = p
	l.mu.Unlock()
	return p, nil
}

// Launches counts the Launch calls for id.
func (l *Launcher) Launches(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.launches[id]
}

// Crash ends the last process launched for id as a crash would: its calls
// fail and it reports Exited.
func (l *Launcher) Crash(id string) {
	l.mu.Lock()
	p := l.running[id]
	l.mu.Unlock()
	if p != nil {
		p.Kill()
	}
}

type process struct {
	client *goplugin.GRPCClient
	kinds  []plugin.Kind
	mu     sync.Mutex
	exited bool
}

func (p *process) Kind(k plugin.Kind) (*plugin.Client, error) {
	return plugin.Dispense(p.client, p.kinds, k)
}

func (p *process) Protocol() int { return plugin.Protocol }

func (p *process) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

func (p *process) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited {
		return
	}
	p.exited = true
	// Close asks the server to stop through go-plugin's controller; stopping
	// it here as well races with that stop.
	_ = p.client.Close()
}
