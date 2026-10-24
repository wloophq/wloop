package factsgithub

import (
	"context"
	"slices"
	"strings"
	"time"

	dchange "github.com/wloophq/wloop/internal/domain/change"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	dview "github.com/wloophq/wloop/internal/domain/view"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// Author is what the views read of u's pull request to project its author
// card; with needLocal, also its author workspace's checkout, as far as
// dview.AuthorColumn may need it. Nil when the store holds no such pull
// request.
func (p *Provider) Author(ctx context.Context, u unit.Unit, needLocal bool) (*dview.AuthorFacts, error) {
	ref, err := refOf(u)
	if err != nil {
		return nil, nil
	}
	v, err := p.fresh(ctx, false)
	if err != nil {
		return nil, err
	}
	row, ok := v.byRef[ref]
	if !ok {
		return nil, nil
	}
	if err := v.loadFacts(ctx, []unit.Unit{u}); err != nil {
		return nil, err
	}
	f, _ := v.factsOf(ref)
	now := p.now()
	af := &dview.AuthorFacts{
		State: f.State, Draft: f.IsDraft, Approved: dchange.Approved(f.Facts), ChecksPass: dchange.ChecksPass(f.Facts),
		ChangesRequestedOnHead: f.ReviewDecision == pr.ReviewChangesRequested && f.NewestChangesRequestOid == f.HeadRefOid,
		Mergeable:              f.Mergeable, Base: f.BaseRefName, ConfirmConflictAfter: v.conflictWindow(),
	}
	if row.Mergeable == "CONFLICTING" && !row.MergeableAt.IsZero() {
		af.ConflictingFor = now.Sub(row.MergeableAt)
	}
	targets := v.targetsOf(ctx)
	_, af.ReviewWorkspace = targets.review[ref]
	w, found := targets.author[ref]
	af.AuthorWorkspace = found && !af.ReviewWorkspace
	if !found {
		w, found = findAuthorWorkspace(v.cfg, v.repos, v.workspaces, f.Facts, v.viewer != "" && strings.EqualFold(f.Author, v.viewer))
	}
	if af.AuthorWorkspace {
		r, err := v.planner(ctx, now).derive(ref)
		if err != nil {
			return nil, err
		}
		af.AuthorWorkspace = !r.address.due && r.address.reason != whyPartial
	}
	if !needLocal || !found {
		return af, nil
	}
	need := localNeed(*af)
	if need == dview.NoLocal {
		return af, nil
	}
	local, err := p.inspect(ctx, v, w, f.HeadRefOid, need == dview.LocalClean)
	if err != nil {
		return nil, err
	}
	af.Local = local
	return af, nil
}

// Carded lists, in order, the pull requests a workspace on the user's board
// targets: those whose cards the facts move without a turn.
func (p *Provider) Carded(ctx context.Context) ([]unit.Unit, error) {
	v, err := p.fresh(ctx, false)
	if err != nil {
		return nil, err
	}
	t := v.targetsOf(ctx)
	var out []unit.Unit
	for _, m := range []map[pr.Ref]wsdomain.Workspace{t.author, t.review} {
		for ref := range m {
			if u := ref.Unit(); !slices.Contains(out, u) {
				out = append(out, u)
			}
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		switch {
		case a.Less(b):
			return -1
		case b.Less(a):
			return 1
		}
		return 0
	})
	return out, nil
}

// Prefetch reads the facts of units in one query, for the Author calls
// that follow on the same view.
func (p *Provider) Prefetch(ctx context.Context, units []unit.Unit) error {
	v, err := p.fresh(ctx, false)
	if err != nil {
		return err
	}
	return v.loadFacts(ctx, units)
}

// localNeed is the most of the checkout dview.AuthorColumn may ask for f
// from any column the card can be in.
func localNeed(f dview.AuthorFacts) dview.LocalNeed {
	need := dview.NoLocal
	for _, current := range []dview.Status{dview.InProgress, dview.InReview, dview.Ready} {
		if _, _, _, n := dview.AuthorColumn(f, current); n > need {
			need = n
		}
	}
	return need
}

// inspect reads the checkout's head, and with clean also its tracked
// changes and whether an agent works there.
func (p *Provider) inspect(ctx context.Context, v *view, w wsdomain.Workspace, prHead string, clean bool) (*dview.LocalFacts, error) {
	l := &dview.LocalFacts{PRHead: prHead}
	if clean {
		st, err := p.d.Git.Status(ctx, w.Path)
		if err != nil {
			return nil, err
		}
		l.Clean, l.Head = !st.Dirty, st.Head
		l.AgentWorking = slices.Contains(v.activity[w.ID].States, dturn.AgentWorking)
	} else {
		head, err := p.d.Git.Head(ctx, w.Path)
		if err != nil {
			return nil, err
		}
		l.Head = head
	}
	if l.Head != prHead && prHead != "" {
		ahead, err := p.d.Git.IsAncestor(ctx, w.Path, prHead, l.Head)
		if err != nil {
			return nil, err
		}
		l.AheadOfPR = ahead
	}
	return l, nil
}

// conflictWindow is how long a conflict must last before its card leaves
// ready, at least five minutes: the fast job stores a conflict and triggers
// the sweep at once.
func (v *view) conflictWindow() time.Duration {
	return max(time.Duration(v.cfg.Sweep.Fast), 5*time.Minute)
}
