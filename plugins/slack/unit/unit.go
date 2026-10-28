// Package unit names the units of work the Slack plugin and the host share:
// a provider and the provider's key, as the host's units are.
package unit

import "github.com/wloophq/wloop/pkg/plugin"

type ProviderID string

type Unit struct {
	Provider ProviderID `json:"provider"`
	Key      string     `json:"key"`
}

func (u Unit) String() string { return string(u.Provider) + ":" + u.Key }

func (u Unit) IsZero() bool { return u == Unit{} }

// Less orders by provider, then key; the sort order of every unit list.
func (u Unit) Less(o Unit) bool {
	if u.Provider != o.Provider {
		return u.Provider < o.Provider
	}
	return u.Key < o.Key
}

// Compare orders a and b as Less does.
func Compare(a, b Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

// Wire is u as the plugin protocol names it.
func (u Unit) Wire() plugin.Unit { return plugin.Unit{Provider: string(u.Provider), Key: u.Key} }

// Of is the unit the protocol names w.
func Of(w plugin.Unit) Unit { return Unit{Provider: ProviderID(w.Provider), Key: w.Key} }
