package ghsource

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/github/ghsourcetest"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/turn"
)

var githubMentionRE = regexp.MustCompile(`@\{github:([^{}\s]+)\}`)

// proposedWho is whom a proposal's text asks, as the nudge intent records it.
func proposedWho(p outbox.Proposal) string {
	return nudged(githubMentionRE.ReplaceAllString(p.Text, "@$1"))
}

// seededNudges is the plugin's nudges over the rows of the seeded store, as
// the daemon hands them to the module.
func seededNudges(t testing.TB, f *storetest.Fixture) (*nudges, readmodel.RowDeps) {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	rd := readmodel.RowDeps{Books: f.Turns, Sessions: f.Turns, Intakes: f.DB.Intakes().Open,
		Settings: func() readmodel.RowSettings {
			return readmodel.RowSettings{Cfg: config.Defaults(), Loaded: l, GateOn: true}
		}}
	n := &nudges{viewer: func(ctx context.Context) (string, error) { return f.Inventory.Viewer(ctx, unit.GitHub) },
		teams: func(ctx context.Context) ([]string, error) { return MyTeams(ctx, f.Inventory) }}
	n.rows(func(rd readmodel.RowDeps) readmodel.Provider {
		return prrow.New(prrow.Source{RowDeps: rd, Store: f.Inventory})
	})(rd)
	return n, rd
}

func TestGitHubNudgeDraftsMatchTheNudgeOffer(t *testing.T) {
	ctx := context.Background()
	f := storetest.Seeded(t, storetest.Live)
	if err := f.Inventory.SetViewer(ctx, unit.GitHub, seededViewer); err != nil {
		t.Fatal(err)
	}
	for i, ref := range f.Open {
		if i%3 == 0 {
			if err := f.Inventory.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindNudged, At: f.Now.Add(-time.Hour), Data: "@someone"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := seededModel(t, f)
	n, _ := seededNudges(t, f)
	props, err := n.Proposals(ctx, f.Now)
	if err != nil {
		t.Fatal(err)
	}
	byUnit := map[unit.Unit][]outbox.Proposal{}
	for _, p := range props {
		byUnit[p.Unit] = append(byUnit[p.Unit], p)
	}
	page, err := m.Rows(ctx, api.ViewAll, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	stalled, enabled, refused := 0, 0, 0
	for _, r := range page.Rows {
		got := byUnit[r.Unit]
		delete(byUnit, r.Unit)
		if !nudgeable(r) {
			if len(got) != 0 {
				t.Errorf("%s (%s): proposed %d nudges for a row that is not stalled", r.Unit.Label(), r.Kind, len(got))
			}
			continue
		}
		stalled++
		rd, ok, err := m.Read(ctx, r.Unit)
		if err != nil || !ok {
			t.Fatalf("reading %s: %v %v", r.Unit.Label(), ok, err)
		}
		base := rd.Base
		base.Pending, base.Asked = nil, catalog.Asked{}
		unmuted, err := rd.Derive(base, prrow.Unmuted(rd.In))
		if err != nil {
			t.Fatal(err)
		}
		p, _ := prrow.PayloadOf(unmuted)
		ref, _ := pr.ParseKey(r.Unit.Key)
		who, why := prrow.NudgeTargets(unmuted, p.Author, seededViewer, prrow.IgnoreOf(config.Defaults()))
		refusal := prrow.NudgeRefusal(ref, who, why, unmuted.Summary.Since, prrow.EventsOf(rd.In), unstartedOf(rd.In), rd.At)
		if st := actionState(r, "nudge"); st == nil || st.Enabled != (refusal == "") {
			t.Errorf("%s: the row's nudge action %+v disagrees with refusal %q", r.Unit.Label(), st, refusal)
		}
		if refusal != "" {
			refused++
			if len(got) != 0 {
				t.Errorf("%s: proposed a nudge the offer refuses: %s", r.Unit.Label(), refusal)
			}
			continue
		}
		enabled++
		if len(got) != 1 {
			t.Errorf("%s: %d nudge proposals, want 1", r.Unit.Label(), len(got))
			continue
		}
		g := got[0]
		if w := strings.Join(who, ", "); proposedWho(g) != w {
			t.Errorf("%s: the proposal asks %q, the offer %q", r.Unit.Label(), proposedWho(g), w)
		}
		if want := (draft.Origin{Kind: draft.OriginAuto, Rule: NudgeRule, Key: r.Unit.String()}); g.Origin != want || !slices.Equal(g.Effects, []string{builtin.EffectPRNudged, builtin.EffectEntryNudged}) {
			t.Errorf("%s: origin %+v effects %v", r.Unit.Label(), g.Origin, g.Effects)
		}
	}
	for u := range byUnit {
		t.Errorf("%s: proposed for a unit no row serves", u.Label())
	}
	if enabled == 0 || refused == 0 {
		t.Fatalf("stalled %d, enabled %d, refused %d: the fixture exercises too little", stalled, enabled, refused)
	}
	t.Logf("%d stalled rows: %d proposals, %d refused", stalled, enabled, refused)
}

func actionState(r api.Row, id string) *api.ActionState {
	i := slices.IndexFunc(r.Actions, func(s api.ActionState) bool { return s.ID == id })
	if i < 0 {
		return nil
	}
	return &r.Actions[i]
}

// stalledChanges is the viewer's changes requested on alice's pull request 1,
// eight days ago: it waits on alice and is stalled.
func stalledChanges() ghsourcetest.Situation {
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(1, ghsourcetest.Other)}
	s.PR.CreatedAt = ghsourcetest.Now.Add(-10 * 24 * time.Hour)
	s.Events = []dinv.Event{ghsourcetest.Event(1, dinv.KindReview, ghsourcetest.Viewer, ghsourcetest.Now.Add(-8*24*time.Hour), "CHANGES_REQUESTED:h1")}
	return s
}

// stalledReview is the viewer's pull request 2 waiting on bob's review for ten days.
func stalledReview() ghsourcetest.Situation {
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(2, ghsourcetest.Viewer)}
	s.PR.CreatedAt, s.PR.ReviewRequests = ghsourcetest.Now.Add(-10*24*time.Hour), []string{ghsourcetest.Reviewer}
	s.Events = []dinv.Event{ghsourcetest.Event(2, dinv.KindReviewRequest, ghsourcetest.Viewer, ghsourcetest.Now.Add(-10*24*time.Hour), ghsourcetest.Reviewer)}
	return s
}

// stalledReviewerBack is the viewer's pull request 3: bob commented five days
// ago, the viewer replied four days ago, and bob has not returned.
func stalledReviewerBack() ghsourcetest.Situation {
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(3, ghsourcetest.Viewer)}
	s.PR.CreatedAt, s.PR.ReviewRequests = ghsourcetest.Now.Add(-10*24*time.Hour), []string{ghsourcetest.Reviewer}
	s.Events = []dinv.Event{
		ghsourcetest.Event(3, dinv.KindReviewRequest, ghsourcetest.Viewer, ghsourcetest.Now.Add(-10*24*time.Hour), ghsourcetest.Reviewer),
		ghsourcetest.Event(3, dinv.KindComment, ghsourcetest.Reviewer, ghsourcetest.Now.Add(-5*24*time.Hour), ""),
		ghsourcetest.Event(3, dinv.KindComment, ghsourcetest.Viewer, ghsourcetest.Now.Add(-4*24*time.Hour), ""),
	}
	return s
}

func propose(s ghsourcetest.Situation) (outbox.Proposal, bool) {
	row := ghsourcetest.Derive(s)
	if !nudgeable(row) {
		return outbox.Proposal{}, false
	}
	s.Override = dinv.Override{}
	return nudgeProposal(row, ghsourcetest.Derive(s), s.Events, nil, ghsourcetest.Viewer, nil, nowOf(s))
}

func nowOf(s ghsourcetest.Situation) time.Time {
	if s.At.IsZero() {
		return ghsourcetest.Now
	}
	return s.At
}

func TestNoNudgeForCardOnlyStates(t *testing.T) {
	for name, s := range map[string]ghsourcetest.Situation{"changes": stalledChanges(), "review": stalledReview()} {
		p, ok := propose(s)
		if !ok {
			t.Fatalf("%s: no nudge for a stalled pull request (%s)", name, ghsourcetest.Derive(s).Kind)
		}
		if p.Text == "" || p.Purpose != nudgePurpose || p.Unit != s.PR.Ref.Unit() {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	draftPR := stalledReview()
	draftPR.PR.IsDraft = true
	hold := stalledReview()
	hold.Override.Hold = true
	muted := stalledReview()
	muted.Override.MutedUntil = ghsourcetest.Now.Add(5 * time.Hour)
	approved := stalledChanges()
	approved.PR.Workspace, approved.PR.ReviewRequests = ghsourcetest.ReviewWorkspace, []string{ghsourcetest.Reviewer}
	approved.Events = append(approved.Events,
		ghsourcetest.Event(1, dinv.KindReviewRequest, ghsourcetest.Other, ghsourcetest.Now.Add(-9*24*time.Hour), ghsourcetest.Reviewer),
		ghsourcetest.Event(1, dinv.KindReview, ghsourcetest.Viewer, ghsourcetest.Now.Add(-7*24*time.Hour), "APPROVED:h1"))
	working := stalledReview()
	working.PR.Workspace = ghsourcetest.ReviewWorkspace
	working.PR.Workspace.AgentStates = []dturn.AgentState{dturn.AgentWorking}
	for name, s := range map[string]ghsourcetest.Situation{"draft": draftPR, "hold": hold, "muted": muted, "approved": approved, "loop working": working} {
		if p, ok := propose(s); ok {
			r := ghsourcetest.Derive(s)
			t.Errorf("%s (%s, views %v): proposed %q", name, r.Kind, r.Views, p.Text)
		}
	}
}

func TestNudgeProposalMentionsWhomTheIntentAsks(t *testing.T) {
	p, ok := propose(stalledChanges())
	if !ok {
		t.Fatal("no proposal")
	}
	if !strings.Contains(p.Text, "@{github:"+ghsourcetest.Other+"}") || proposedWho(p) != "@"+ghsourcetest.Other {
		t.Fatalf("text %q", p.Text)
	}
	if !slices.Equal(p.Items, []string{"gh:fix"}) {
		t.Fatalf("items %v", p.Items)
	}
	team := stalledReview()
	team.PR.ReviewRequests = []string{"team:acme/web"}
	team.Events[0].Data = "team:acme/web"
	p, ok = propose(team)
	if !ok || !strings.Contains(p.Text, "@acme/web ") || strings.Contains(p.Text, "@{") || proposedWho(p) != "@acme/web" {
		t.Fatalf("team proposal %v %q", ok, p.Text)
	}
}

func TestIntentNudgeAndDraftNudgeShareTheWindow(t *testing.T) {
	s := stalledChanges()
	if _, ok := propose(s); !ok {
		t.Fatal("no proposal before any nudge")
	}
	intent := s
	intent.Events = append(slices.Clone(s.Events), ghsourcetest.Event(1, dinv.KindNudged, "", ghsourcetest.Now.Add(-3*time.Hour), "@"+ghsourcetest.Other))
	if p, ok := propose(intent); ok {
		t.Fatalf("proposed %q within %s of an intent nudge", p.Text, prrow.RenudgeAfter)
	}
	later := intent
	later.At = ghsourcetest.Now.Add(prrow.RenudgeAfter)
	if _, ok := propose(later); !ok {
		t.Fatal("no proposal once the window passed")
	}

	p, f, evs := testPublisher(t)
	prop, _ := propose(s)
	d := draft.Draft{ID: 3, Unit: prop.Unit, Purpose: prop.Purpose, Effects: prop.Effects, Text: prop.Text, State: draft.Sending}
	f.now = ghsourcetest.Now
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	sent := s
	sent.Events = append(slices.Clone(s.Events), evs.evs...)
	if p, ok := propose(sent); ok {
		t.Fatalf("proposed %q again after a draft nudge was sent", p.Text)
	}
	row := ghsourcetest.Derive(sent)
	refusal := prrow.NudgeRefusal(s.PR.Ref, []string{"@" + ghsourcetest.Other}, "", row.Summary.Since, sent.Events, nil, ghsourcetest.Now)
	if !strings.Contains(refusal, "already nudged @"+ghsourcetest.Other) {
		t.Fatalf("intent refusal %q", refusal)
	}
	if st := actionState(row, "nudge"); st == nil || st.Enabled || !strings.Contains(st.Reason, "already nudged @"+ghsourcetest.Other) {
		t.Fatalf("nudge action %+v", st)
	}
}

func TestDraftNudgeCommentIsANudgeComment(t *testing.T) {
	s := stalledReviewerBack()
	p, f, evs := testPublisher(t)
	f.now = ghsourcetest.Now
	prop, ok := propose(s)
	if !ok {
		t.Fatal("no proposal")
	}
	ev, err := p.Publish(context.Background(), draft.Draft{ID: 4, Unit: prop.Unit, Purpose: prop.Purpose, Effects: prop.Effects, Text: prop.Text, State: draft.Sending})
	if err != nil {
		t.Fatal(err)
	}
	comment := ghsourcetest.Event(3, dinv.KindComment, ghsourcetest.Viewer, ev.At.Add(time.Second), "")
	viaDraft := s
	viaDraft.Events = append(slices.Clone(s.Events), append(slices.Clone(evs.evs), comment)...)
	viaIntent := s
	viaIntent.Events = append(slices.Clone(s.Events),
		ghsourcetest.Event(3, dinv.KindNudged, "", ghsourcetest.Now, "@"+ghsourcetest.Reviewer), comment)
	bare := s
	bare.Events = append(slices.Clone(s.Events), comment)

	sit := func(s ghsourcetest.Situation) string {
		r := ghsourcetest.Derive(s)
		return string(r.Kind) + "|" + r.Summary.WaitsOn + "|" + r.Summary.Since.String() + "|" + r.Summary.Reason
	}
	if got, want := sit(viaDraft), sit(viaIntent); got != want {
		t.Fatalf("after a draft nudge %s, after an intent nudge %s", got, want)
	}
	if got, want := sit(viaDraft), sit(s); got != want {
		t.Fatalf("the draft nudge's comment moved the situation: %s, before %s", got, want)
	}
	if sit(bare) == sit(s) {
		t.Fatalf("a plain comment leaves %s too; the case proves nothing", sit(bare))
	}
}

func BenchmarkGitHubProposals(b *testing.B) {
	ctx := context.Background()
	f := storetest.Seeded(b, storetest.Live)
	if err := f.Inventory.SetViewer(ctx, unit.GitHub, seededViewer); err != nil {
		b.Fatal(err)
	}
	n, _ := seededNudges(b, f)
	for b.Loop() {
		if _, err := n.Proposals(ctx, f.Now); err != nil {
			b.Fatal(err)
		}
	}
}

func TestGitHubNudgeDraftsThroughTheOutbox(t *testing.T) {
	ctx := context.Background()
	f := storetest.Seeded(t, storetest.Live)
	if err := f.Inventory.SetViewer(ctx, unit.GitHub, seededViewer); err != nil {
		t.Fatal(err)
	}
	n, _ := seededNudges(t, f)
	props, err := n.Proposals(ctx, f.Now)
	if err != nil || len(props) < 4 {
		t.Fatalf("%d proposals, %v", len(props), err)
	}
	p, gh, _ := testPublisher(t)
	p.Events, p.Now = f.Inventory, func() time.Time { return f.Now }
	gh.viewer, gh.now = seededViewer, f.Now
	p.Viewer = func(ctx context.Context) (string, error) { return f.Inventory.Viewer(ctx, unit.GitHub) }
	svc := &outbox.Service{Store: f.DB.Drafts(), Clock: fixedClock(f.Now),
		Effects: func(unit.ProviderID) (port.Effects, error) { return p, nil }}
	issueOf := func(u unit.Unit) string {
		ref, _ := pr.ParseKey(u.Key)
		return "/repos/" + ref.Owner + "/" + ref.Repo + "/issues/" + strconv.Itoa(ref.Number)
	}

	sentProp := props[0]
	d, err := svc.Propose(ctx, sentProp)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != draft.Proposed || gh.posts() != 0 {
		t.Fatalf("proposing posted: %+v, %d posts", d.State, gh.posts())
	}
	if _, err := svc.Propose(ctx, sentProp); !errors.Is(err, outbox.ErrDuplicate) {
		t.Fatalf("a second proposal of the same pull request: %v", err)
	}
	if sent, failed, err := svc.Send(ctx); sent+failed != 0 || err != nil {
		t.Fatalf("sent an unapproved draft: %d %d %v", sent, failed, err)
	}
	if _, err := svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := svc.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("send %d %v", sent, err)
	}
	if got := gh.posted(issueOf(sentProp.Unit)); len(got) != 1 {
		t.Fatalf("posted %+v", got)
	}
	ref, _ := pr.ParseKey(sentProp.Unit.Key)
	last, ok, err := f.Inventory.LastEvent(ctx, ref, dinv.KindNudged)
	if err != nil || !ok || last.Data != proposedWho(sentProp) {
		t.Fatalf("nudged event %+v %v %v, want data %q", last, ok, err, proposedWho(sentProp))
	}
	again, err := n.Proposals(ctx, f.Now)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(again, func(p outbox.Proposal) bool { return p.Unit == sentProp.Unit }) {
		t.Fatalf("%s proposed again right after its nudge was sent", sentProp.Unit.Label())
	}

	crashed, err := svc.Propose(ctx, props[1])
	if err != nil {
		t.Fatal(err)
	}
	if crashed, err = svc.Approve(ctx, crashed.ID, nil); err != nil {
		t.Fatal(err)
	}
	crashed.State, crashed.SendingAt = draft.Sending, f.Now
	if crashed, err = f.DB.Drafts().Update(ctx, crashed, draft.Approved); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(ctx, crashed); err != nil {
		t.Fatal(err)
	}
	posts := gh.posts()
	if err := svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.DB.Drafts().Get(ctx, crashed.ID)
	if err != nil || got.State != draft.Sent || got.Evidence.Permalink == "" {
		t.Fatalf("recovered %+v %v", got, err)
	}
	if gh.posts() != posts {
		t.Fatal("recovery posted again")
	}

	failing, err := svc.Propose(ctx, props[2])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, failing.ID, nil); err != nil {
		t.Fatal(err)
	}
	gh.fail = http.StatusBadGateway
	if _, failed, err := svc.Send(ctx); failed != 1 || err != nil {
		t.Fatalf("failed %d %v", failed, err)
	}
	got, err = f.DB.Drafts().Get(ctx, failing.ID)
	if err != nil || got.State != draft.Failed || !strings.HasPrefix(got.Error, mayHaveBeenPosted) {
		t.Fatalf("failed draft %+v %v", got, err)
	}
	if _, err := svc.Retry(ctx, failing.ID, false); !errors.Is(err, outbox.ErrNeedsConfirm) {
		t.Fatalf("retry without confirm: %v", err)
	}

	refused, err := svc.Propose(ctx, props[3])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, refused.ID, nil); err != nil {
		t.Fatal(err)
	}
	refusedRef, _ := pr.ParseKey(props[3].Unit.Key)
	lastBefore, hadBefore, err := f.Inventory.LastEvent(ctx, refusedRef, dinv.KindNudged)
	if err != nil {
		t.Fatal(err)
	}
	gh.fail = http.StatusUnprocessableEntity
	if _, failed, err := svc.Send(ctx); failed != 1 || err != nil {
		t.Fatalf("failed %d %v", failed, err)
	}
	got, err = f.DB.Drafts().Get(ctx, refused.ID)
	if err != nil || got.State != draft.Failed || strings.HasPrefix(got.Error, mayHaveBeenPosted) {
		t.Fatalf("refused draft %+v %v", got, err)
	}
	lastAfter, hasAfter, err := f.Inventory.LastEvent(ctx, refusedRef, dinv.KindNudged)
	if err != nil || hasAfter != hadBefore || lastAfter.At != lastBefore.At {
		t.Fatalf("a nudge GitHub refused recorded %+v (before %+v)", lastAfter, lastBefore)
	}
	again, err = n.Proposals(ctx, f.Now)
	if err != nil || !slices.ContainsFunc(again, func(p outbox.Proposal) bool { return p.Unit == props[3].Unit }) {
		t.Fatalf("a refused nudge closed the renudge window: %v", err)
	}
}
