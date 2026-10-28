package source

import (
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Broker is the host's side of the broker of one source plugin: what the
// plugin calls back. Each broker interface lives in its own file; a method
// the host does not serve yet answers ErrUnsupported.
type Broker struct {
	// ID is the plugin's id, the provider of its units.
	ID string
	// Manifest is the plugin's [source] table.
	Manifest *manifest.Source
	Units    Units
	// Snapshots keep what the plugin reports.
	Snapshots *Snapshots
	// Config is the configuration in force.
	Config func() config.Config
}

// Host is b as the SDK serves it to the plugin.
func (b *Broker) Host() src.Host {
	return src.Host{Reporter: reporter{b}, Ledger: ledger{b}, Reader: reader{b}, Gates: gates{b}, Recorder: recorder{b},
		Wakes: wakes{b}, Services: services{b}, History: history{b}}
}
