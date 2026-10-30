package ghsource

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/situation"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/nudge"
)

var githubMentionRE = regexp.MustCompile(`@\{github:([^{}\s]+)\}`)

// pluginProposals are the GitHub plugin's nudge proposals over the seeded
// store's pull requests, reading the host's books and overrides through the
// broker, as the running plugin does.
func pluginProposals(t *testing.T, f *storetest.Fixture) []src.Draft {
	t.Helper()
	ctx := context.Background()
	rows, err := ghreporttest.Inventory{Store: f.Inventory, Now: f.Inventory.Now}.Rows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := &psource.Broker{ID: string(unit.GitHub), Units: psource.Units{Codecs: unit.NewRegistry(pr.Codec{})}, Books: f.Turns.Books, Overrides: f.DB,
		Hosted: &psource.Hosted{Tasks: f.DB.Tasks()}}
	ds, err := nudge.Proposals(ctx, rows, b.Host().Reader, ghreporttest.NudgeTexts(), prrow.IgnoreOf(config.Defaults()), f.Now)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

// muteByWake mutes ref until a time as the mute action does: a wake on an
// asleep task that holds it.
func muteByWake(t *testing.T, f *storetest.Fixture, ref pr.Ref, until time.Time) {
	t.Helper()
	ctx := context.Background()
	id := task.DerivedID(ref.Unit(), "mute")
	e := task.Task{ID: id, State: "scheduled", Category: task.CategoryDeferred, Units: []unit.Unit{ref.Unit()}, Origin: task.OriginUser,
		Created: f.Now, Updated: f.Now}
	if err := f.DB.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Wakes().Put(ctx, wake.Wake{Task: id, Until: until, Return: "next"}); err != nil {
		t.Fatal(err)
	}
}

// TestGitHubNudgeDraftsMatchTheNudgeOffer holds the plugin's github-nudge
// proposals to the host's rows: a proposal for each stalled row whose nudge
// action is enabled, asking whom that action would ask, and none for any
// other row.
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
		var set map[string]string
		switch i % 7 {
		case 1:
			set = map[string]string{port.OverrideHold: port.OverrideTrue}
		case 2:
			muteByWake(t, f, ref, f.Now.Add(time.Hour))
		}
		if set != nil {
			if _, err := f.DB.SetOverride(ctx, ref.Unit(), set, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := seededModel(t, f)
	byUnit := map[unit.Unit][]src.Draft{}
	for _, d := range pluginProposals(t, f) {
		u := unit.Unit{Provider: unit.ProviderID(d.Unit.Provider), Key: d.Unit.Key}
		byUnit[u] = append(byUnit[u], d)
	}
	page, err := m.Rows(ctx, api.Since{})
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
		base.Plans, base.Asked = nil, catalog.Asked{}
		unmuted, err := rd.Derive(base, prrow.Unmuted(rd.In))
		if err != nil {
			t.Fatal(err)
		}
		p, _ := prrow.PayloadOf(unmuted)
		ref, _ := pr.ParseKey(r.Unit.Key)
		who, why := prrow.NudgeTargets(unmuted, p.Author, seededViewer, prrow.IgnoreOf(config.Defaults()))
		b, _ := prrow.BookOf(rd.In)
		refusal := prrow.NudgeRefusal(ref, who, why, unmuted.Summary.Since, prrow.EventsOf(rd.In), prrow.Unstarted(b), rd.At)
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
		if w, asked := strings.Join(who, ", "), nudge.Who(githubMentionRE.ReplaceAllString(got[0].Text, "@$1")); asked != w {
			t.Errorf("%s: the proposal asks %q, the offer %q", r.Unit.Label(), asked, w)
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

// nudgeable says whether the host's row has its stalled signal on and is
// not one the card alone shows (draft, approved by the viewer, on hold,
// muted), as the plugin's github-nudge rule reads the plugin's signal.
func nudgeable(row api.Row) bool {
	p, ok := prrow.PayloadOf(row)
	if !ok || !prrow.Signals(row)["stalled"] {
		return false
	}
	switch situation.Kind(row.Kind) {
	case situation.KindMuted, situation.KindDraft, situation.KindDone:
		return false
	}
	return !p.Approved && !p.Override.Hold && p.Mute == nil
}

func actionState(r api.Row, id string) *api.ActionState {
	i := slices.IndexFunc(r.Actions, func(s api.ActionState) bool { return s.ID == id })
	if i < 0 {
		return nil
	}
	return &r.Actions[i]
}
