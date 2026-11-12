package github

import (
	"context"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/publish"
)

var (
	_ src.Publisher = (*Plugin)(nil)
	_ src.Drafter   = (*Plugin)(nil)
)

func (p *Plugin) publisher() (*publish.Publisher, error) {
	r, err := p.running()
	if err != nil {
		return nil, err
	}
	return r.publisher(), nil
}

// publisher posts over the plugin's client: the viewer its store keeps,
// the persons the host's items of a unit name, the nudged events on the
// host.
func (r *running) publisher() *publish.Publisher {
	pub := &publish.Publisher{GH: r.client, Threads: r.client, Viewer: r.db.Viewer, Now: r.now, Log: logf}
	if r.host.Ledger != nil {
		pub.People = func(ctx context.Context, u sdk.Unit) ([]src.Person, error) {
			its, err := r.host.Ledger.QueryItems(ctx, src.ItemQuery{Units: []sdk.Unit{u}})
			if err != nil {
				return nil, err
			}
			var out []src.Person
			for _, it := range its {
				out = append(append(append(out, it.Requester), it.Assignees...), it.WaitingOn...)
			}
			return out, nil
		}
	}
	if r.host.Recorder != nil {
		pub.Record = func(ctx context.Context, ev src.Event) error {
			return r.host.Recorder.RecordEvents(ctx, []src.Event{ev})
		}
	}
	return pub
}

func (p *Plugin) Validate(ctx context.Context, d src.Draft) error {
	pub, err := p.publisher()
	if err != nil {
		return err
	}
	return pub.Validate(ctx, d)
}

func (p *Plugin) Where(ctx context.Context, d src.Draft) (string, error) {
	pub, err := p.publisher()
	if err != nil {
		return "", err
	}
	return pub.Where(ctx, d)
}

func (p *Plugin) Place(ctx context.Context, d src.Draft) (src.Placed, error) {
	pub, err := p.publisher()
	if err != nil {
		return src.Placed{}, err
	}
	return pub.Place(ctx, d)
}

func (p *Plugin) Publish(ctx context.Context, d src.Draft) (src.Evidence, error) {
	pub, err := p.publisher()
	if err != nil {
		return src.Evidence{}, err
	}
	return pub.Publish(ctx, d)
}

func (p *Plugin) Verify(ctx context.Context, d src.Draft, startedAt time.Time) (src.Evidence, bool, error) {
	pub, err := p.publisher()
	if err != nil {
		return src.Evidence{}, false, err
	}
	return pub.Verify(ctx, d, startedAt)
}

// Basis is u's head commit as GitHub has it now, never the store's: what a
// review on u would be written against.
func (p *Plugin) Basis(ctx context.Context, u sdk.Unit, _ string) (string, error) {
	pub, err := p.publisher()
	if err != nil {
		return "", err
	}
	ref, err := refOf(u)
	if err != nil {
		return "", err
	}
	return pub.Head(ctx, ref)
}

// Within says whether target lies inside u's pull request: the pull
// request, one of its review comments or one of its review threads.
func (p *Plugin) Within(ctx context.Context, u sdk.Unit, target string) (bool, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return false, nil
	}
	pub, err := p.publisher()
	if err != nil {
		return false, err
	}
	return pub.Within(ctx, ref, target)
}
