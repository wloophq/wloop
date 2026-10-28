package present

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
)

// prepareNudge refuses when nobody but the user is to be asked, or the
// same wait was already nudged, and fills who, the situation and whether
// it asks the maintainers into the call's inputs.
func (p *Presenter) prepareNudge(ctx context.Context, u unit.Unit, args api.PluginActionArgs) (map[string]any, error) {
	if args.Unmuted == nil {
		return nil, api.Refuse(http.StatusNotFound, "%s is not in the inventory", u.Label())
	}
	r := *args.Unmuted
	ref, ok := pr.RefOf(r.Unit)
	if !ok {
		return nil, api.Refuse(http.StatusBadRequest, "%s is not a pull request", u.Label())
	}
	base, at, err := p.h.Model.Base(ctx)
	if err != nil {
		return nil, err
	}
	d := p.deriverAt(base, at)
	evs, err := p.d.Store.Events(ctx, ref)
	if err != nil {
		return nil, err
	}
	var book turn.Book
	if p.h.Books != nil {
		books, err := p.h.Books(ctx, r.Unit)
		if err != nil {
			return nil, err
		}
		book = books[r.Unit]
	}
	pl, _ := prrow.PayloadOf(r)
	who, why := prrow.NudgeTargets(r, pl.Author, d.Viewer, d.Ignore)
	if refusal := prrow.NudgeRefusal(ref, who, why, r.Summary.Since, evs, prrow.Unstarted(book), d.Now); refusal != "" {
		return nil, api.Refuse(http.StatusBadRequest, "%s", refusal)
	}
	inputs := maps.Clone(args.Inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	inputs["situation"] = nudgeSituation(who, r)
	inputs["waits_on_maintainers"] = slices.Equal(who, []string{prrow.NudgeMaintainers})
	inputs["who"] = slices.Clone(who)
	return inputs, nil
}

func nudgeSituation(who []string, r api.Row) string {
	since := ""
	if !r.Summary.Since.IsZero() {
		since = " since " + r.Summary.Since.Local().Format(time.RFC3339)
	}
	// The kind's label, not the reason: a reason can carry CI job names.
	return fmt.Sprintf("Waiting on: %s%s, %s.", strings.Join(who, ", "), since, situation.Kind(r.Kind).Label())
}
