package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/core/automation"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

// entryCloser is what entry.close reads and closes (*coregtd.Service).
type entryCloser interface {
	Entry(ctx context.Context, id gtd.ID) (gtd.Entry, error)
	Close(ctx context.Context, id gtd.ID, to gtd.State) error
}

var errNoEntries = errors.New("entry.close needs the daemon's entries")

func (s *stepSet) entryCloseCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	if s.d.Entries == nil {
		return "", errNoEntries
	}
	id, ok := gtd.IDOf(c.u)
	if !ok {
		return fmt.Sprintf("%s is not an entry", c.u), nil
	}
	e, err := s.d.Entries.Entry(ctx, id)
	switch {
	case errors.Is(err, coregtd.ErrNoEntry):
		return "the entry is gone", nil
	case err != nil:
		return "", err
	case e.Origin == gtd.OriginDerived:
		return "a derived entry closes when its items resolve", nil
	case !e.Live():
		return "the entry is already " + string(e.State), nil
	}
	return "", nil
}

func (s *stepSet) entryCloseRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	if refusal, err := s.entryCloseCheck(ctx, c, automation.CheckOpts{}); err != nil || refusal != "" {
		if err == nil {
			err = automation.Refused{Reason: refusal}
		}
		return "", err
	}
	id, _ := gtd.IDOf(c.u)
	if err := s.d.Entries.Close(ctx, id, gtd.Done); err != nil {
		return "", err
	}
	return "closed the entry", nil
}
