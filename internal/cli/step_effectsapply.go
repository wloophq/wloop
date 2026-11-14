package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// silentApplier is the Outbox's application of effects with no draft (*outbox.Service).
type silentApplier interface {
	SilentlyApplicable(p unit.ProviderID, effects []string) error
	ApplySilently(ctx context.Context, u unit.Unit, effects []string) error
}

func (s *stepSet) effectsApplyCheck(_ context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	a, err := s.silentApplier()
	if err != nil {
		return err.Error(), nil
	}
	if err := a.SilentlyApplicable(c.u.Provider, c.list("effects")); err != nil {
		return err.Error(), nil
	}
	return "", nil
}

func (s *stepSet) effectsApplyRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	a, err := s.silentApplier()
	if err != nil {
		return "", automation.Refused{Reason: err.Error()}
	}
	effects := c.list("effects")
	switch err := a.ApplySilently(ctx, c.u, effects); {
	case errors.Is(err, outbox.ErrInvalid):
		return "", automation.Refused{Reason: err.Error()}
	case errors.Is(err, src.ErrUnavailable):
		return "", automation.Transient{Err: err}
	case err != nil:
		return "", err
	}
	return fmt.Sprintf("applied %s on %s with no message", strings.Join(effects, ", "), c.u), nil
}

func (s *stepSet) silentApplier() (silentApplier, error) {
	if s.d.Drafts == nil {
		return nil, errNoDraftDeps
	}
	ob, err := s.d.Drafts.outbox()
	if err != nil {
		return nil, err
	}
	a, ok := ob.(silentApplier)
	if !ok {
		return nil, errors.New("the Outbox applies no effect without a draft")
	}
	return a, nil
}
