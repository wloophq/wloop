package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gtd"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type startedHeadless struct{ got []headless.Request }

func (h *startedHeadless) Start(_ context.Context, r headless.Request) (int64, error) {
	h.got = append(h.got, r)
	return 42, nil
}

func newActServer(t *testing.T, enabled func(unit.ProviderID) bool) (*Server, *startedHeadless) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &startedHeadless{}
	return newServerOn(t, db, nil, func(d *Deps) { d.Headless, d.PluginEnabled = h, enabled }), h
}

const r7Act = "/api/units/github/acme%2Fapi%237/act"

func TestActActionStartsHeadless(t *testing.T) {
	t.Parallel()
	s, h := newThreadActServer(t)
	rec := post(t, s, threadAct, `{"items":["i1"],"guidance":"keep it short"}`)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got map[string]int64
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got["seq"] != 42 {
		t.Fatalf("%s %v", rec.Body, err)
	}
	if len(h.got) != 1 || h.got[0].Unit != actThread || !slices.Equal(h.got[0].Items, []string{"i1"}) || h.got[0].Guidance != "keep it short" || h.got[0].Trigger != turn.ByHand {
		t.Fatalf("started %+v", h.got)
	}
	pr, ph := newActServer(t, nil)
	if rec := post(t, pr, r7Act, `{"items":["i1"]}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "Not yours") || len(ph.got) != 0 {
		t.Fatalf("someone else's pull request: %d %s", rec.Code, rec.Body)
	}
}

func TestActRefusedForDisabledPlugin(t *testing.T) {
	t.Parallel()
	s, h := newActServer(t, func(p unit.ProviderID) bool { return p != unit.GitHub })
	rec := post(t, s, r7Act, `{"items":["i1"]}`)
	if rec.Code != 409 || len(h.got) != 0 {
		t.Fatalf("%d %s started %d", rec.Code, rec.Body, len(h.got))
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["plugin"] != "github" {
		t.Fatalf("%s %v", rec.Body, err)
	}
}

func TestGitHubDelegateRunsReviewWithActLoaded(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	row, err := c.s.currentRow(context.Background(), r7u)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(row.Actions, func(st api.ActionState) bool { return st.ID == string(dintent.Act) }) {
		t.Fatalf("the row does not offer act: %+v", row.Actions)
	}
	if st, ok := c.s.offeredFor(row, gtd.VerbDelegate).runs(); !ok || st.ID != string(dintent.Review) {
		t.Fatalf("delegate runs %q (%v), want review", st.ID, ok)
	}
	rec := post(t, c.s, api.EntryPath("e-r", "delegate"), `{"source":"cockpit"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "review accepted as intake") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

type refusingHeadless struct{ err error }

func (h refusingHeadless) Start(context.Context, headless.Request) (int64, error) { return 0, h.err }

func TestActAnswers503WhenTheAgentCannotRunRestricted(t *testing.T) {
	t.Parallel()
	s, _ := newThreadActServer(t)
	s.deps.Headless = refusingHeadless{turns.ErrNoRestricted}
	if rec := post(t, s, threadAct, `{}`); rec.Code != 503 || !strings.Contains(rec.Body.String(), "restricted") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

var actThread = unit.Unit{Provider: "thread", Key: "1700000000.1"}

const threadAct = "/api/units/thread/1700000000.1/act"

func newThreadActServer(t *testing.T) (*Server, *startedHeadless) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &startedHeadless{}
	return newServerOn(t, db, nil, func(d *Deps) {
		d.Headless, d.Actions = h, &fakeActions{}
		d.Units = unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "thread"})
	}), h
}

func TestActRunsOneModeDecisionWhateverTheCaller(t *testing.T) {
	t.Parallel()
	s, h := newThreadActServer(t)
	fa := s.deps.Actions.(*fakeActions)
	if rec := post(t, s, threadAct, `{"items":["i1"]}`); rec.Code != 202 || len(h.got) != 1 || h.got[0].Intent != turn.Intent(dintent.Act) {
		t.Fatalf("the act route: %d %s %+v", rec.Code, rec.Body, h.got)
	}
	bridged := apiRequest("POST", "/api/units/thread/1700000000.1/act", strings.NewReader(`{"source":"cockpit","items":["i1"]}`))
	bridged.SetPathValue("provider", "thread")
	bridged.SetPathValue("key", actThread.Key)
	bridged.SetPathValue("action", string(dintent.Act))
	rec := httptest.NewRecorder()
	s.handleAction(rec, bridged)
	if rec.Code != 202 || len(h.got) != 2 || len(fa.calls) != 0 {
		t.Fatalf("the generic action path, as the GTD bridge calls it: %d %s, started %d, session calls %v", rec.Code, rec.Body, len(h.got), fa.calls)
	}
	if rec := post(t, s, threadAct, `{"mode":"session","guidance":"look"}`); rec.Code != 200 || len(fa.calls) != 1 || !strings.HasPrefix(fa.calls[0], "act ") || len(h.got) != 2 {
		t.Fatalf("session mode: %d %s, calls %v, started %d", rec.Code, rec.Body, fa.calls, len(h.got))
	}
	if rec := post(t, s, threadAct, `{"mode":"later"}`); rec.Code != 400 {
		t.Fatalf("an unknown mode: %d %s", rec.Code, rec.Body)
	}
}

func TestTheBridgeDelegatesActHeadlessWithTheEntrysItems(t *testing.T) {
	t.Parallel()
	s, h := newThreadActServer(t)
	store := gtdtest.New()
	g := coregtd.New(coregtd.Deps{Store: store, Facts: coregtd.CardFacts{}, Clock: fixedClock(catalogNow)})
	e := gtd.Entry{ID: "e-t", Kind: gtd.KindAction, State: gtd.Inbox, Units: []unit.Unit{actThread}, Items: []gtd.ItemRef{{Unit: actThread, Item: "i1"}},
		Origin: gtd.OriginDerived, Created: catalogNow, Updated: catalogNow}
	if err := store.Apply(context.Background(), []gtd.Change{{Op: gtd.OpCreate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.runBridge(rec, apiRequest("POST", api.EntryPath("e-t", "delegate"), nil), g, e, actThread, api.ActionState{ID: string(dintent.Act), Enabled: true}, api.EntryRequest{Source: "cockpit"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "act started headless as turn 42") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(h.got) != 1 || !slices.Equal(h.got[0].Items, []string{"i1"}) || h.got[0].Entry != "e-t" {
		t.Fatalf("started %+v", h.got)
	}
}
