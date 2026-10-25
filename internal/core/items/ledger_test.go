package items

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestDisabledErrorNamesTheProvider(t *testing.T) {
	if got := (&DisabledError{Provider: "slack"}).Error(); got != "slack is disabled" {
		t.Errorf("%q", got)
	}
}

func TestApplyRefusesOpWithoutAnchor(t *testing.T) {
	s := newMem()
	l, _ := newLedger(s)
	op := create(thread, "", "x")
	op.Anchor = unit.Anchor{}
	res := apply(t, l, items.Extracted, op)
	if len(res.Refused) != 1 || res.Refused[0].Reason != "anchor: required" || len(res.Applied) != 0 {
		t.Fatalf("%+v", res)
	}
	if len(s.items) != 0 || !slices.Equal(s.outcomes(), []string{OutcomeRefused}) {
		t.Errorf("items %v, log %v", s.items, s.outcomes())
	}
}

func TestApplyRefusesBadOps(t *testing.T) {
	l, _ := newLedger(newMem())
	apply(t, l, items.User, create(thread, "i1", "x"))
	noKind := create(thread, "", "x")
	noKind.Fields.Kind = nil
	noSummary := create(thread, "", "x")
	noSummary.Fields.Summary = nil
	res := apply(t, l, items.User,
		update(thread, "i9", "y", "m:2"),
		noKind,
		noSummary,
		items.Op{Op: items.OpEvidence, Unit: thread, Item: "i1", Anchor: anchor("draft:1:reply")},
	)
	want := []string{`item: unknown "i9"`, "fields.kind: required", "fields.summary: required", "evidence: required"}
	if len(res.Refused) != len(want) {
		t.Fatalf("%+v", res.Refused)
	}
	for i, r := range res.Refused {
		if r.Index != i || !strings.Contains(r.Reason, want[i]) {
			t.Errorf("refusal %d: %+v, want %q", i, r, want[i])
		}
	}
}

func TestOpsBuildAnItem(t *testing.T) {
	s := newMem()
	l, _ := newLedger(s)
	me := items.Person{Key: "slack:U000AAA", Name: "me"}
	res := apply(t, l, items.Extracted,
		create(thread, "", "review the plan"),
		items.Op{Op: items.OpAssign, Unit: thread, Item: "i1", Anchor: anchor("C000AAA/1.3"), Assignees: []items.Person{me}, Basis: items.ExplicitMention},
		items.Op{Op: items.OpResolve, Unit: thread, Item: "i1", Anchor: anchor("C000AAA/1.4"), Resolution: items.Done},
	)
	if len(res.Applied) != 3 {
		t.Fatalf("%+v", res)
	}
	it := itemOf(t, l, thread, "i1")
	if !reflect.DeepEqual(it.Assignees, []items.Person{me}) || it.AssignedAt.Ref != "C000AAA/1.3" || it.AssignedAt.Unit != thread {
		t.Errorf("assigned %+v at %+v", it.Assignees, it.AssignedAt)
	}
	if it.State != items.Resolved || it.Resolution != items.Done {
		t.Errorf("state %s %s", it.State, it.Resolution)
	}
	if it.Evidence.Kind != items.EvidenceMessage || it.Evidence.Anchor.Ref != "C000AAA/1.4" {
		t.Errorf("evidence %+v", it.Evidence)
	}
	if !slices.Equal(s.outcomes(), []string{OutcomeApplied, OutcomeApplied, OutcomeApplied}) {
		t.Errorf("log %v", s.outcomes())
	}
}

func TestReopenClearsResolution(t *testing.T) {
	l, _ := newLedger(newMem())
	apply(t, l, items.Extracted, create(thread, "i1", "x"),
		items.Op{Op: items.OpResolve, Unit: thread, Item: "i1", Anchor: anchor("m:2"), Resolution: items.Answered})
	apply(t, l, items.Extracted, items.Op{Op: items.OpReopen, Unit: thread, Item: "i1", Anchor: anchor("m:3")})
	it := itemOf(t, l, thread, "i1")
	if it.State != items.Open || it.Resolution != "" || it.Evidence != (items.Evidence{}) {
		t.Errorf("%+v", it)
	}
}

func TestCreateAssignsStableIDs(t *testing.T) {
	s := newMem()
	l, _ := newLedger(s)
	res := apply(t, l, items.Extracted, create(thread, "", "a"), create(thread, "", "b"), create(pull, "", "c"))
	var ids []string
	for _, a := range res.Applied {
		ids = append(ids, a.Item.ID)
	}
	if !slices.Equal(ids, []string{"i1", "i2", "i1"}) {
		t.Fatalf("ids %v", ids)
	}
	l2, _ := newLedger(s)
	res = apply(t, l2, items.Extracted, create(thread, "", "d"))
	if got := res.Applied[0].Item.ID; got != "i3" {
		t.Errorf("after reload: %s", got)
	}
}

func TestRepeatedCreateIsNoop(t *testing.T) {
	s := newMem()
	l, c := newLedger(s)
	apply(t, l, items.Extracted, create(thread, "i1", "x"))
	before := itemOf(t, l, thread, "i1")
	c.t = c.t.Add(time.Hour)
	res := apply(t, l, items.Extracted, create(thread, "i1", "x"))
	if len(res.Applied) != 1 || res.Applied[0].Changed {
		t.Fatalf("%+v", res)
	}
	if got := itemOf(t, l, thread, "i1"); !reflect.DeepEqual(got, before) {
		t.Errorf("changed:\n%+v\n%+v", got, before)
	}
	if !slices.Equal(s.outcomes(), []string{OutcomeApplied, OutcomeNoop}) {
		t.Errorf("log %v", s.outcomes())
	}
}

func TestUserOpConfirms(t *testing.T) {
	s := newMem()
	l, c := newLedger(s)
	apply(t, l, items.Extracted, create(thread, "i1", "x"))
	c.t = c.t.Add(time.Minute)
	apply(t, l, items.User, update(thread, "i1", "y", "user:1"))
	it := itemOf(t, l, thread, "i1")
	if !it.Confirmed || !it.UserAt.Equal(c.t) || it.Summary != "y" {
		t.Errorf("%+v", it)
	}
	if last := s.log[len(s.log)-1]; last.Provenance != items.User || last.Outcome != OutcomeApplied {
		t.Errorf("log %+v", last)
	}
}

func TestExtractionOnConfirmedProposes(t *testing.T) {
	s := newMem()
	l, _ := newLedger(s)
	apply(t, l, items.Extracted, create(thread, "i1", "x"))
	apply(t, l, items.User, update(thread, "i1", "corrected", "user:1"))
	res := apply(t, l, items.Extracted, update(thread, "i1", "extracted again", "m:5"))
	if len(res.Proposed) != 1 || res.Proposed[0].ID == 0 || len(res.Applied) != 0 {
		t.Fatalf("%+v", res)
	}
	if got := itemOf(t, l, thread, "i1").Summary; got != "corrected" {
		t.Errorf("summary %q", got)
	}
	res = apply(t, l, items.Extracted, update(thread, "i1", "extracted again", "m:6"))
	if len(res.Proposed) != 0 {
		t.Errorf("second proposal %+v", res.Proposed)
	}
	open, _ := l.Proposals(context.Background(), ProposalOpen)
	if len(open) != 1 || open[0].Op.Fields == nil || *open[0].Op.Fields.Summary != "extracted again" {
		t.Errorf("open %+v", open)
	}
}

func TestIdenticalOpIsNoop(t *testing.T) {
	s := newMem()
	l, _ := newLedger(s)
	apply(t, l, items.Extracted, create(thread, "i1", "x"))
	apply(t, l, items.User, update(thread, "i1", "y", "user:1"))
	res := apply(t, l, items.Extracted, update(thread, "i1", "y", "m:9"))
	if len(res.Proposed) != 0 || len(res.Applied) != 1 || res.Applied[0].Changed {
		t.Fatalf("%+v", res)
	}
	if s.log[len(s.log)-1].Outcome != OutcomeNoop || len(s.proposals) != 0 {
		t.Errorf("log %v, proposals %v", s.outcomes(), s.proposals)
	}
}

func TestUnitEventResolvesConfirmed(t *testing.T) {
	l, _ := newLedger(newMem())
	apply(t, l, items.User, create(pull, "i1", "x"))
	ev := items.Evidence{Kind: items.EvidenceUnitEvent, Anchor: unit.Anchor{Unit: pull, Ref: "merged"}}
	res := apply(t, l, items.Derived, items.Op{Op: items.OpResolve, Unit: pull, Item: "i1", Anchor: anchor("merged"), Resolution: items.Done, Evidence: &ev})
	if len(res.Applied) != 1 || !res.Applied[0].Changed {
		t.Fatalf("%+v", res)
	}
	if it := itemOf(t, l, pull, "i1"); it.State != items.Resolved || !it.Confirmed {
		t.Errorf("%+v", it)
	}
	apply(t, l, items.User, items.Op{Op: items.OpReopen, Unit: pull, Item: "i1", Anchor: anchor("user:2")})
	claimed := apply(t, l, items.Extracted, items.Op{Op: items.OpResolve, Unit: pull, Item: "i1", Anchor: anchor("m:3"), Resolution: items.Done, Evidence: &ev})
	if len(claimed.Proposed) != 1 || itemOf(t, l, pull, "i1").State != items.Open {
		t.Errorf("an extracted unit_event resolve changed a confirmed item: %+v", claimed)
	}
	apply(t, l, items.Derived, items.Op{Op: items.OpResolve, Unit: pull, Item: "i1", Anchor: anchor("merged"), Resolution: items.Done, Evidence: &ev})
	other := apply(t, l, items.Derived, items.Op{Op: items.OpReopen, Unit: pull, Item: "i1", Anchor: anchor("row:2")})
	if len(other.Proposed) != 1 {
		t.Errorf("a derived reopen of a confirmed item applied: %+v", other)
	}
}

func TestDecideProposal(t *testing.T) {
	ctx := context.Background()
	s := newMem()
	l, _ := newLedger(s)
	apply(t, l, items.User, create(thread, "i1", "x"))
	p1 := apply(t, l, items.Extracted, update(thread, "i1", "a", "m:1")).Proposed[0]
	p2 := apply(t, l, items.Turn, update(thread, "i1", "b", "turn:1")).Proposed[0]
	res, err := l.Decide(ctx, p1.ID, true)
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	it := itemOf(t, l, thread, "i1")
	if it.Summary != "a" || !it.Confirmed {
		t.Errorf("%+v", it)
	}
	if last := s.log[len(s.log)-1]; last.Provenance != items.User {
		t.Errorf("accepted as %s", last.Provenance)
	}
	if _, err := l.Decide(ctx, p2.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := itemOf(t, l, thread, "i1").Summary; got != "a" {
		t.Errorf("rejected changed it: %q", got)
	}
	all, _ := l.Proposals(ctx, "")
	if len(all) != 2 || all[0].State != ProposalAccepted || all[1].State != ProposalRejected {
		t.Errorf("%+v", all)
	}
	if _, err := l.Decide(ctx, p1.ID, true); !errors.Is(err, ErrNoProposal) {
		t.Errorf("deciding twice: %v", err)
	}
}

func derived(id, summary string) items.Item {
	return items.Item{ID: id, Kind: items.Request, Summary: summary}
}

func TestReconcileCreates(t *testing.T) {
	l, _ := newLedger(newMem())
	res, err := l.Reconcile(context.Background(), pull, []items.Item{derived("gh:review", "review")}, anchor("row:1"), "")
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	it := itemOf(t, l, pull, "gh:review")
	if it.Provenance != items.Derived || it.State != items.Open || it.Unit != pull || it.Origin.Ref != "row:1" {
		t.Errorf("%+v", it)
	}
}

func TestReconcileResolvesAbsentWithGivenResolution(t *testing.T) {
	ctx := context.Background()
	for _, absent := range []items.Resolution{"", items.Done} {
		l, _ := newLedger(newMem())
		if _, err := l.Reconcile(ctx, pull, []items.Item{derived("gh:review", "r"), derived("gh:fix", "f")}, anchor("row:1"), ""); err != nil {
			t.Fatal(err)
		}
		apply(t, l, items.Extracted, create(pull, "i1", "extracted"))
		if _, err := l.Reconcile(ctx, pull, []items.Item{derived("gh:review", "r")}, anchor("row:2"), absent); err != nil {
			t.Fatal(err)
		}
		want := absent
		if want == "" {
			want = items.Superseded
		}
		it := itemOf(t, l, pull, "gh:fix")
		if it.State != items.Resolved || it.Resolution != want || it.Evidence.Kind != items.EvidenceUnitEvent || it.Evidence.Anchor.Ref != "row:2" {
			t.Errorf("absent %q: %+v", absent, it)
		}
		if got := itemOf(t, l, pull, "i1"); got.State != items.Open {
			t.Errorf("extracted item touched: %+v", got)
		}
		if got := itemOf(t, l, pull, "gh:review"); got.State != items.Open {
			t.Errorf("desired item resolved: %+v", got)
		}
	}
}

func TestReconcileIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newMem()
	l, c := newLedger(s)
	desired := []items.Item{derived("gh:review", "r"), {ID: "gh:merge", Kind: items.Todo, Summary: "m", State: items.Resolved, Resolution: items.Done}}
	if _, err := l.Reconcile(ctx, pull, desired, anchor("row:1"), ""); err != nil {
		t.Fatal(err)
	}
	commits := s.commits
	c.t = c.t.Add(time.Hour)
	res, err := l.Reconcile(ctx, pull, desired, anchor("row:2"), "")
	if err != nil || len(res.Applied) != 0 || s.commits != commits {
		t.Errorf("%+v %v, commits %d -> %d", res, err, commits, s.commits)
	}
}

func TestReconcileOnConfirmedNeverProposes(t *testing.T) {
	ctx := context.Background()
	s := newMem()
	l, _ := newLedger(s)
	if _, err := l.Reconcile(ctx, pull, []items.Item{derived("gh:review", "r")}, anchor("row:1"), ""); err != nil {
		t.Fatal(err)
	}
	apply(t, l, items.User, update(pull, "gh:review", "mine", "user:1"))
	for rev := range 10 {
		if _, err := l.Reconcile(ctx, pull, []items.Item{derived("gh:review", "r"+strings.Repeat("!", rev))}, anchor("row:x"), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := itemOf(t, l, pull, "gh:review").Summary; got != "mine" {
		t.Errorf("summary %q", got)
	}
	last := s.log[len(s.log)-1]
	if last.Outcome != OutcomeNoop || last.Reason != "confirmed" || len(s.proposals) != 0 {
		t.Errorf("log %+v, proposals %v", last, s.proposals)
	}
	logged := len(s.log)
	for range 10 {
		if _, err := l.Reconcile(ctx, pull, []items.Item{derived("gh:review", "same")}, anchor("row:y"), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(s.log) - logged; got != 1 {
		t.Errorf("10 identical reconciles logged %d rows, want 1", got)
	}
}

func TestLinkOpWritesLink(t *testing.T) {
	links := &fakeLinks{}
	l, _ := newLedger(newMem())
	l.Links = links
	apply(t, l, items.Turn, create(thread, "i1", "review o/r#1"))
	op := items.Op{Op: items.OpLink, Unit: thread, Item: "i1", Anchor: anchor("C000AAA/1.3"), To: &pull, LinkKind: link.Review}
	res := apply(t, l, items.Turn, op)
	if len(res.Applied) != 1 || len(links.put) != 1 {
		t.Fatalf("%+v %+v", res, links.put)
	}
	got := links.put[0]
	if got.From != thread || got.To != pull || got.Kind != link.Review || got.Item != "i1" || got.Origin != link.Extraction || got.Evidence.Ref != "C000AAA/1.3" {
		t.Errorf("%+v", got)
	}
	apply(t, l, items.Turn, op)
	if len(links.put) != 1 {
		t.Errorf("repeated link put again")
	}
	apply(t, l, items.User, items.Op{Op: items.OpLink, Unit: thread, Anchor: anchor("user:1"), To: &pull, LinkKind: link.Mention})
	if len(links.put) != 2 || links.put[1].Origin != link.User || links.put[1].Item != "" {
		t.Errorf("%+v", links.put)
	}
}

func TestLinkOpRefusals(t *testing.T) {
	l, _ := newLedger(newMem())
	apply(t, l, items.Turn, create(thread, "i1", "x"))
	op := items.Op{Op: items.OpLink, Unit: thread, Item: "i1", Anchor: anchor("m:1"), To: &pull, LinkKind: link.Review}
	if res := apply(t, l, items.Turn, op); len(res.Refused) != 1 {
		t.Errorf("nil Links: %+v", res)
	}
	l.Links = &fakeLinks{}
	op.LinkKind = "closes"
	if res := apply(t, l, items.Turn, op); len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Reason, "link_kind") {
		t.Errorf("bad kind: %+v", res)
	}
}

func TestEvidenceOpOnlyAppends(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		s := newMem()
		l, _ := newLedger(s)
		prov := items.Extracted
		if confirmed {
			prov = items.User
		}
		apply(t, l, prov, create(thread, "i1", "x"))
		before := itemOf(t, l, thread, "i1")
		ev := items.Evidence{Kind: items.EvidenceSentDraft, Anchor: unit.Anchor{Ref: "draft:7:reply"}}
		res := apply(t, l, items.Effects, items.Op{Op: items.OpEvidence, Unit: thread, Item: "i1", Anchor: anchor("draft:7:reply"), Evidence: &ev})
		if len(res.Applied) != 1 || len(res.Proposed) != 0 || len(s.proposals) != 0 {
			t.Fatalf("%+v", res)
		}
		after := itemOf(t, l, thread, "i1")
		if len(after.Trail) != 1 || after.Trail[0].Kind != items.EvidenceSentDraft || after.Trail[0].Anchor.Unit != thread {
			t.Errorf("trail %+v", after.Trail)
		}
		after.Trail, after.UpdatedAt = nil, before.UpdatedAt
		if !reflect.DeepEqual(after, before) {
			t.Errorf("confirmed=%v changed more than the trail:\n%+v\n%+v", confirmed, after, before)
		}
	}
}

func TestDisabledProviderRefusesWholeCall(t *testing.T) {
	ctx := context.Background()
	s := newMem()
	l, _ := newLedger(s)
	apply(t, l, items.User, create(pull, "i1", "x"))
	p := apply(t, l, items.Extracted, update(pull, "i1", "y", "m:1")).Proposed[0]
	commits, logged := s.commits, len(s.log)
	l.Enabled = func(p unit.ProviderID) bool { return p != unit.GitHub }
	var de *DisabledError
	if _, err := l.Apply(ctx, items.User, []items.Op{create(thread, "", "ok"), update(pull, "i1", "z", "user:2")}); !errors.As(err, &de) || de.Provider != unit.GitHub {
		t.Errorf("apply: %v", err)
	}
	if _, err := l.Decide(ctx, p.ID, true); !errors.As(err, &de) {
		t.Errorf("decide: %v", err)
	}
	if _, err := l.Reconcile(ctx, pull, nil, anchor("row:1"), ""); !errors.As(err, &de) {
		t.Errorf("reconcile: %v", err)
	}
	if s.commits != commits || len(s.log) != logged || len(s.items[thread]) != 0 {
		t.Errorf("wrote: commits %d -> %d", commits, s.commits)
	}
}

func TestALinkToADisabledProvidersUnitIsStored(t *testing.T) {
	ctx := context.Background()
	links := &fakeLinks{}
	l, _ := newLedger(newMem())
	l.Links = links
	l.Enabled = func(p unit.ProviderID) bool { return p != unit.GitHub }
	res, err := l.Apply(ctx, items.User, []items.Op{{Op: items.OpLink, Unit: thread, Anchor: anchor("user:3"), To: &pull, LinkKind: link.Mention}})
	if err != nil || len(res.Applied) != 1 || len(links.put) != 1 || links.put[0].To != pull {
		t.Fatalf("%+v %v %+v", res, err, links.put)
	}
	var de *DisabledError
	if _, err := l.Apply(ctx, items.User, []items.Op{{Op: items.OpLink, Unit: pull, Anchor: anchor("user:4"), To: &thread, LinkKind: link.Mention}}); !errors.As(err, &de) || de.Provider != unit.GitHub {
		t.Errorf("a link from a disabled unit: %v", err)
	}
}

func TestLinkTimeHonorsAnchorOnlyWhenExtracted(t *testing.T) {
	old := t0.Add(-365 * 24 * time.Hour)
	recent := t0.Add(-2 * time.Hour)
	cases := []struct {
		name   string
		prov   items.Provenance
		anchor time.Time
		want   time.Time
	}{
		{"turn backdated", items.Turn, old, t0},
		{"user backdated", items.User, recent, t0},
		{"extracted recent", items.Extracted, recent, recent},
		{"extracted before lookback", items.Extracted, old, t0.Add(-ExtractedLinkLookback)},
		{"extracted future", items.Extracted, t0.Add(time.Hour), t0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			links := &fakeLinks{}
			l, _ := newLedger(newMem())
			l.Links = links
			apply(t, l, items.Extracted, create(thread, "i1", "review o/r#1"))
			a := anchor("C000AAA/1.3")
			a.At = c.anchor
			res := apply(t, l, c.prov, items.Op{Op: items.OpLink, Unit: thread, Item: "i1", Anchor: a, To: &pull, LinkKind: link.Review})
			if len(res.Applied) != 1 || len(links.put) != 1 {
				t.Fatalf("%+v", res)
			}
			if got := links.put[0].At; !got.Equal(c.want) {
				t.Errorf("At = %v, want %v", got, c.want)
			}
		})
	}
}
