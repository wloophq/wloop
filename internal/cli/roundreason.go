package cli

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dchange "github.com/wloophq/wloop/internal/domain/change"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/turn"
)

// automatedReason is the round the planner decided: its reviewers, failing
// checks and conflict, each check with the summary and link of the read.
func automatedReason(f workflow.Facts, read dpr.Facts) *turn.RoundReason {
	checks := read.Checks
	var r turn.RoundReason
	for _, who := range factList(f, "pr.address.reviewers") {
		r.Reviewers = append(r.Reviewers, turn.RoundReviewer{Login: who})
	}
	for _, name := range factList(f, "pr.address.checks") {
		c := turn.FailingCheck{Name: name}
		if i := slices.IndexFunc(checks, func(c dpr.Check) bool { return c.Name == name }); i >= 0 {
			c.Summary, c.URL = checks[i].Summary, checks[i].URL
		}
		r.Checks = append(r.Checks, c)
	}
	if conflict, _ := f["pr.address.conflict"].(bool); conflict {
		r.Conflict = &turn.RoundConflict{Base: read.BaseRefName}
	}
	return nonEmpty(r)
}

// handReason is what a round asked for by hand finds: the reviews a round
// would answer with none before it, marked handled unless they came after
// the last round, and the checks failing and the conflict now, each marked
// handled when the last round covered this head.
func handReason(f dpr.Facts, viewer string, handled time.Time, checksHead, conflictHead string, cfg config.Config) *turn.RoundReason {
	in := dchange.AddressInput{PR: f, Viewer: viewer, SkipReviewers: cfg.AddressSkipReviewers}
	all, _ := dchange.ReviewsToAddress(in)
	in.Handled = handled
	since, _ := dchange.ReviewsToAddress(in)
	for _, who := range since {
		if !containsLogin(all, who) {
			all = append(all, who)
		}
	}
	var r turn.RoundReason
	for _, who := range all {
		r.Reviewers = append(r.Reviewers, turn.RoundReviewer{Login: who, Handled: !containsLogin(since, who)})
	}
	for _, c := range f.Checks {
		if !c.Failed || containsLogin(cfg.IgnoreChecks, c.Name) || slices.ContainsFunc(r.Checks, func(x turn.FailingCheck) bool { return x.Name == c.Name }) {
			continue
		}
		r.Checks = append(r.Checks, turn.FailingCheck{Name: c.Name, Summary: c.Summary, URL: c.URL, Handled: f.HeadRefOid != "" && f.HeadRefOid == checksHead})
	}
	if f.Mergeable == "CONFLICTING" {
		r.Conflict = &turn.RoundConflict{Base: f.BaseRefName, Handled: f.HeadRefOid != "" && f.HeadRefOid == conflictHead}
	}
	return nonEmpty(r)
}

func nonEmpty(r turn.RoundReason) *turn.RoundReason {
	if len(r.Reviewers) == 0 && len(r.Checks) == 0 && r.Conflict == nil {
		return nil
	}
	return &r
}

func containsLogin(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}

func factList(f workflow.Facts, name string) []string { return inputList(f[name]) }

// withAddressReason gives the request of an intent whose blocks select by
// reviewer what its round finds, from the inventory's full read and the
// round state; without a full read the turn goes without it.
func (e *Env) withAddressReason(ctx context.Context, cfg config.Config, d dintent.Def, r *turn.Request) {
	p, ok := r.Payload.(*turn.PR)
	if !ok || p.Address != nil || !slices.Contains(d.Dims, dintent.DimReviewer) {
		return
	}
	ref := prOf(r.Unit)
	full, viewer, err := e.coveredPR(ctx, ref)
	if err != nil {
		return
	}
	var handled time.Time
	var checksHead, conflictHead string
	if e.Rounds != nil {
		if st, err := e.Rounds.Load(ctx); err == nil {
			handled, _ = time.Parse(time.RFC3339Nano, st.Mark(ref.String(), round.AddressedReviews))
			checksHead, conflictHead = st.Mark(ref.String(), round.AddressedChecks), st.Mark(ref.String(), round.AddressedConflict)
		}
	}
	p.Address = handReason(full.Facts, viewer, handled, checksHead, conflictHead, cfg.For(ref.Owner, ref.Repo))
}
