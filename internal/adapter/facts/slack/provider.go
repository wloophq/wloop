// Package factslack puts the Slack units the extraction stores behind the
// engine's Units, the Slack plugin's workflow facts (Module.Facts): every
// stored Slack unit with a live entry, with its facts in slot slack.
package factslack

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// ErrNotImplemented is what the provider answers until it lands.
var ErrNotImplemented = errors.New("factslack: not implemented")

// Provider is the slack subject's facts provider.
type Provider struct{}

var _ automation.Units = Provider{}

func (Provider) List(context.Context, unit.ProviderID) ([]unit.Unit, error) {
	return nil, ErrNotImplemented
}

func (Provider) Facts(context.Context, unit.Unit) (workflow.Facts, error) {
	return nil, ErrNotImplemented
}
