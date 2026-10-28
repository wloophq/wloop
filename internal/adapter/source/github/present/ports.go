package present

import (
	"context"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// Records are the store's turns, briefs and unitless events as the daemon
// reads them.
type Records struct{ Store *inventory.Store }

func (r Records) Turn(ctx context.Context, id string) (port.SentText, bool, error) {
	return inventory.ByUnit{Store: r.Store}.Turn(ctx, id)
}

func (r Records) UnitlessEvents(ctx context.Context) ([]port.Event, error) {
	return inventory.ByUnit{Store: r.Store}.UnitlessEvents(ctx)
}

// Brief is u's stored brief, with how long its last run took.
func (r Records) Brief(ctx context.Context, u unit.Unit) (intake.StoredBrief, bool, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return intake.StoredBrief{}, false, nil
	}
	rec, ok, err := r.Store.BriefRecord(ctx, ref)
	if err != nil || !ok {
		return intake.StoredBrief{}, false, err
	}
	b := intake.StoredBrief{State: prrow.BriefState(rec, true), JSON: rec.Brief}
	if run, ok, err := r.Store.BriefRun(ctx, rec.Run); err == nil && ok && !run.EndedAt.IsZero() {
		b.Took = run.EndedAt.Sub(run.StartedAt)
	}
	return b, true, nil
}

// Queue is the brief queue keyed by pull request.
type Queue interface {
	Run(ctx context.Context) (string, error)
	Touched(refs []pr.Ref)
	Due() <-chan struct{}
	Forget(ctx context.Context, ref pr.Ref) error
	Attempt(ctx context.Context, ref pr.Ref) (int, error)
}

// BriefQueue is a brief queue keyed by unit, as the daemon runs it; a unit
// that is no pull request has no brief.
type BriefQueue struct{ Q Queue }

func (b BriefQueue) Run(ctx context.Context) (string, error) { return b.Q.Run(ctx) }

func (b BriefQueue) Due() <-chan struct{} { return b.Q.Due() }

func (b BriefQueue) Touched(units []unit.Unit) {
	var refs []pr.Ref
	for _, u := range units {
		if ref, ok := pr.RefOf(u); ok {
			refs = append(refs, ref)
		}
	}
	if len(refs) > 0 {
		b.Q.Touched(refs)
	}
}

func (b BriefQueue) Forget(ctx context.Context, u unit.Unit) error {
	ref, ok := pr.RefOf(u)
	if !ok {
		return nil
	}
	return b.Q.Forget(ctx, ref)
}

func (b BriefQueue) Attempt(ctx context.Context, u unit.Unit) (int, error) {
	ref, _ := pr.RefOf(u)
	return b.Q.Attempt(ctx, ref)
}

// Repo is the repository of a pull request, owner/name; "" for any other
// unit.
func Repo(_ context.Context, u unit.Unit) (string, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return "", nil
	}
	return ref.Owner + "/" + ref.Repo, nil
}
