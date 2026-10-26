// Package factentry puts the GTD entries behind the engine's Units, as the
// core-kept entry provider: every entry neither done nor in the trash, of
// providers whose plugin is enabled, with its facts in slot entry.
package factentry

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// ErrNotImplemented is what the provider answers until it lands.
var ErrNotImplemented = errors.New("factentry: not implemented")

// Provider is the entry subject's facts provider. It names the entry's own
// unit as the subject's owner and wakes the sweep at the next nudge-due or
// resurface time.
type Provider struct{}

var (
	_ automation.Units = Provider{}
	_ automation.Owner = Provider{}
	_ automation.Dueer = Provider{}
)

func (Provider) List(context.Context, unit.ProviderID) ([]unit.Unit, error) {
	return nil, ErrNotImplemented
}

func (Provider) Facts(context.Context, unit.Unit) (workflow.Facts, error) {
	return nil, ErrNotImplemented
}

func (Provider) Owners(context.Context, unit.Unit) ([]unit.Unit, error) { return nil, nil }

func (Provider) Due() time.Time { return time.Time{} }
