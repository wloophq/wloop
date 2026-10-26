package tui

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func gtdCatalog() api.GTDCatalog {
	views := []api.GTDView{{ID: "focus", Label: "Focus"}, {ID: "inbox", Label: "Inbox"}, {ID: "outbox", Label: "Outbox"},
		{ID: "next", Label: "Next"}, {ID: "waiting", Label: "Waiting"}, {ID: "projects", Label: "Projects"},
		{ID: "someday", Label: "Someday"}, {ID: "logbook", Label: "Logbook"}}
	verbs := []api.GTDVerb{{Verb: "next", Label: "Next", Key: "x"}, {Verb: "waiting", Label: "Waiting", Key: "w"},
		{Verb: "schedule", Label: "Schedule", Key: "s"}, {Verb: "someday", Label: "Someday", Key: "y"},
		{Verb: "reference", Label: "Reference", Key: "l"}, {Verb: "focus", Label: "Focus", Key: "f"},
		{Verb: "project", Label: "Project", Key: "p"}, {Verb: "approve", Label: "Approve", Key: "a"},
		{Verb: "delegate", Label: "Delegate", Key: "d"}, {Verb: "done", Label: "Done", Key: "e"},
		{Verb: "dismiss", Label: "Dismiss", Key: "Backspace"}, {Verb: "seen", Label: "Seen"}}
	return api.GTDCatalog{Views: views, Areas: []string{"work", "home"}, Contexts: []string{"computer", "calls"}, Verbs: verbs}
}

var allVerbs = []string{"next", "waiting", "schedule", "someday", "reference", "focus", "project", "approve", "delegate", "done", "dismiss"}

func verbs(enabled ...string) []api.VerbState {
	var out []api.VerbState
	for _, v := range allVerbs {
		st := api.VerbState{Verb: v, Enabled: slices.Contains(enabled, v)}
		if !st.Enabled {
			st.Reason = v + " is not offered here"
		}
		out = append(out, st)
	}
	return out
}

func entry(id, title, state string, contexts ...string) api.EntryView {
	return api.EntryView{ID: id, Kind: "action", State: state, Title: title, Contexts: contexts,
		Verbs: verbs(allVerbs...), Created: now.Add(-30 * time.Hour), Updated: now.Add(-5 * time.Hour)}
}

func waitingOn(e api.EntryView, names ...string) api.EntryView {
	w := &api.WaitingView{Since: now.Add(-3 * 24 * time.Hour)}
	for _, n := range names {
		w.On = append(w.On, api.WaitingPerson{Name: n})
	}
	e.Waiting = w
	return e
}

// gtdEntries are the entries of each GTD view.
func gtdEntries() map[string][]api.EntryView {
	focus := entry("e1", "Reply on the batch export review", "next", "computer")
	focus.Focus, focus.Units = true, []unit.Unit{ref(7).Unit()}
	asked := entry("e2", "Deploy window tonight?", "inbox")
	asked.Changed, asked.Verbs = true, verbs("next", "someday", "reference", "dismiss", "done", "focus", "waiting", "schedule", "project")
	asked.Message = &api.Message{ID: 4, EntryID: "e2", Kind: "question", Text: "Which window?", State: "open", Recommend: "late",
		Options: []record.Option{{ID: "early", Label: "Before six"}, {ID: "late", Label: "After ten"}}}
	captured := entry("e3", "Buy cables", "inbox")
	draft := entry("draft:11", "Reply in the release thread", "outbox")
	draft.Kind, draft.Verbs = "draft", verbs("approve", "dismiss")
	draft.Draft = &api.Draft{ID: 11, Rev: 4, Kind: "reply", Purpose: "reply", Target: "C0REL", Where: "#releases thread", Text: "Ship it tonight.\nRollback plan in the doc.",
		State: "pending", Approval: api.DraftApproval{Mode: "wait"}, Origin: api.DraftOrigin{Kind: "turn"}}
	return map[string][]api.EntryView{
		"focus":  {focus},
		"inbox":  {asked, captured},
		"outbox": {draft},
		"next": {entry("e4", "Call the bank", "next", "calls"), entry("e5", "Rename the export flag", "next", "computer"),
			entry("e6", "Sort the receipts", "next"), entry("e7", "Write the runbook", "next", "computer")},
		"waiting": {waitingOn(entry("e8", "Contract signature", "waiting"), "bob"), waitingOn(entry("e9", "Design review", "waiting"), "ana"),
			waitingOn(entry("e10", "Reimbursement", "waiting")), waitingOn(entry("e11", "Laptop order", "waiting"), "ana")},
	}
}

func gtdCounters() api.Counters {
	return api.Counters{Views: map[string]int{"focus": 1, "inbox": 2, "outbox": 1, "next": 4, "waiting": 4, "projects": 0, "someday": 0, "logbook": 12},
		Areas: map[string]int{}, Contexts: map[string]int{}, Badge: 3}
}

// gtdModel is a model with the GTD views loaded, on Focus.
func gtdModel(t testing.TB, rs ...api.Row) *Model {
	t.Helper()
	m := newModel(t)
	m.SetData(rs, api.Health{})
	m.SetGTD(gtdCatalog())
	for v, es := range gtdEntries() {
		m.SetEntries(v, es, gtdCounters())
	}
	return m
}

func chord(m *Model, keys string) (Command, bool) {
	var ks []Key
	for _, r := range keys {
		if r != ' ' {
			ks = append(ks, Key{Rune: r})
		}
	}
	return press(m, ks...)
}

func screenOf(m *Model, w, h int) string { return strings.Join(m.Render(w, h, now), "\n") }

func TestStartsOnFocus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registerBoard(t)
		m := gtdModel(t)
		m.SetPlugins(boardPlugins(true))
		if v, ok := m.gtdView(); !ok || v.ID != "focus" {
			t.Fatalf("started on view %d", m.view)
		}
		tabs := m.Render(220, 20, now)[0]
		order := []string{"[Focus 1]", "Inbox 2", "Outbox 1", "Next 4", "Waiting 4", "Projects 0", "Someday 0", "Logbook 12", "Needs me 0", "Plan 0", "Board"}
		at := -1
		for _, want := range order {
			i := strings.Index(tabs, want)
			if i <= at {
				t.Fatalf("%q missing or out of order in %q", want, tabs)
			}
			at = i
		}
		if narrow := m.Render(120, 20, now)[0]; strings.Contains(narrow, "Needs me") || !strings.Contains(narrow, "Logbook 12") {
			t.Fatalf("a narrow tab line keeps the GTD views whole and drops the others: %q", narrow)
		}

		d := &fakeDaemon{rows: oneRow(), gtd: ptr(gtdCatalog()), entries: gtdEntries(), counters: gtdCounters()}
		term, done := start(t, d)
		eventually(t, "Focus", func() bool {
			s := term.screen()
			return strings.Contains(s, "[Focus 1]") && strings.Contains(s, "Reply on the batch export review")
		})
		quit(t, term, done)
	})
}

func ptr[T any](v T) *T { return &v }

func TestDisabledPluginHasNoTUIView(t *testing.T) {
	registerBoard(t)
	specs := servedSpecs(t)
	specs.Specs = slices.DeleteFunc(specs.Specs, func(s api.ActionSpec) bool { return s.Plugin == string(unit.GitHub) })
	m := NewModel()
	m.SetCatalog(api.CatalogResponse{})
	m.SetSpecs(specs.Specs)
	m.SetKeys(servedKeys(specs))
	r := row(7, "Batch export", "review", "needs-me")
	m.SetData([]api.Row{r}, api.Health{})
	m.SetPlugins([]api.Plugin{{ID: unit.GitHub, Name: "GitHub"}, {ID: boardProvider}})
	m.SetGTD(gtdCatalog())
	for v, es := range gtdEntries() {
		m.SetEntries(v, es, gtdCounters())
	}
	screen := screenOf(m, 220, 30)
	for _, gone := range []string{"Needs me", "Board", "V review", "F address"} {
		if strings.Contains(screen, gone) {
			t.Errorf("a disabled plugin's %q is shown:\n%s", gone, screen)
		}
	}
	for _, k := range []rune{'V', 'F', 'N', 'A'} {
		if c, ok := press(m, Key{Rune: k}); ok {
			t.Errorf("%c sent %+v", k, c)
		}
	}
	chord(m, "gg")
	if v, ok := m.gtdView(); !ok || v.ID != "focus" || !strings.Contains(m.status, "no GitHub views") {
		t.Fatalf("g g with GitHub disabled: view %d, status %q", m.view, m.status)
	}
}

func TestFakeProviderRowRendersInTUI(t *testing.T) {
	m := gtdModel(t)
	c := api.CatalogResponse{Views: []api.ViewSpec{{ID: "threads", Label: "Threads", Provider: "thread", Rows: true, Surfaces: []string{"tui"}}},
		Situations: []api.SituationSpec{{Provider: "thread", Kind: "waiting", Label: "waiting on you"}}}
	m.SetCatalog(c)
	th := threadRow()
	th.Views = []string{"threads"}
	m.SetData([]api.Row{th}, api.Health{})
	chord(m, "gg")
	if !strings.Contains(m.status, "no github view") {
		t.Errorf("g g with only a thread view: %q", m.status)
	}
	m.setView(0)
	screen := screenOf(m, 120, 30)
	for _, want := range []string{"[Threads 1]", "#general", "Deploy window?", "me · waiting on you", "ana asked you"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	for _, field := range []string{"brief", "workspace", "checks", "author"} {
		if strings.Contains(screen, "  "+field+" ") {
			t.Errorf("a thread row shows the pull request field %q:\n%s", field, screen)
		}
	}
}

func TestGoGitHubSkipsOtherProviders(t *testing.T) {
	m := gtdModel(t)
	m.SetCatalog(api.CatalogResponse{Views: []api.ViewSpec{
		{ID: "threads", Label: "Threads", Provider: "thread", Rows: true, Order: 0, Surfaces: []string{"tui"}},
		{ID: "needs-me", Label: "Needs me", Provider: "github", Rows: true, Order: 1, Surfaces: []string{"tui"}}}})
	chord(m, "gg")
	if m.view != 1 || m.views[m.view].ID != "needs-me" {
		t.Fatalf("g g landed on view %d %+v", m.view, m.views)
	}
}

func TestOutboxInTUI(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow(), gtd: ptr(gtdCatalog()), entries: gtdEntries(), counters: gtdCounters(), doLag: 300 * time.Millisecond}
		term, done := start(t, d)
		eventually(t, "Focus", func() bool { return strings.Contains(term.screen(), "[Focus 1]") })
		term.press(Key{Rune: 'g'}, Key{Rune: 'o'})
		eventually(t, "the draft", func() bool {
			s := term.screen()
			return strings.Contains(s, "[Outbox 1]") && strings.Contains(s, "#releases thread") && strings.Contains(s, "Ship it tonight.")
		})
		term.press(Key{Rune: 'a'})
		eventually(t, "the refusal from the list", func() bool { return strings.Contains(term.screen(), "open the draft to approve it") })
		term.press(Key{Name: KeyEnter})
		eventually(t, "the whole text", func() bool {
			s := term.screen()
			return strings.Contains(s, "Ship it tonight.") && strings.Contains(s, "Rollback plan in the doc.")
		})
		term.press(Key{Rune: 'a'})
		eventually(t, "the confirm", func() bool { return strings.Contains(term.screen(), "post to #releases thread? y confirms") })
		if n := len(d.dos()); n != 0 {
			t.Fatalf("%d verbs sent before the y", n)
		}
		term.press(Key{Rune: 'y'})
		eventually(t, "the approval", func() bool { return len(d.dos()) == 1 && strings.Contains(term.screen(), "approve done") })
		term.press(Key{Name: KeyBackspace})
		eventually(t, "the discard confirm", func() bool { return strings.Contains(term.screen(), "y confirms") })
		term.press(Key{Rune: 'y'})
		eventually(t, "the discard", func() bool { return len(d.dos()) == 2 })
		text := map[string]string{"text": "Ship it tonight.\nRollback plan in the doc.", "rev": "4"}
		want := []entryDo{{"draft:11", "approve", "tui", text}, {"draft:11", "dismiss", "tui", nil}}
		if got := d.dos(); !reflect.DeepEqual(got, want) {
			t.Fatalf("sent %+v", got)
		}
		quit(t, term, done)
	})
}

func TestOutboxApprovesTheTextOnScreen(t *testing.T) {
	m := gtdModel(t)
	chord(m, "go")
	if _, ok := press(m, Key{Rune: 'a'}, Key{Rune: 'y'}); ok {
		t.Fatal("approved from the list")
	}
	e := gtdEntries()["outbox"][0]
	press(m, Key{Name: KeyEnter})
	m.SetDetail(e.ID, e, nil)
	edited := e
	edited.Updated = e.Updated.Add(time.Minute)
	edited.Draft = ptr(*e.Draft)
	edited.Draft.Text = "Ship it now, no rollback."
	edited.Draft.Rev = 5
	m.SetEntries("outbox", []api.EntryView{edited}, gtdCounters())
	if _, ok := press(m, Key{Rune: 'a'}); ok || m.prompt == nil || !m.prompt.confirm {
		t.Fatalf("a did not ask: %+v", m.prompt)
	}
	c, ok := press(m, Key{Rune: 'y'})
	if want := map[string]string{"text": e.Draft.Text, "rev": "4"}; !ok || c.verb != "approve" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("approved %+v %v, want the text on screen", c, ok)
	}
}

func (f *fakeDaemon) dos() []entryDo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.entryDos)
}

func TestGChordsSwitchViews(t *testing.T) {
	m := gtdModel(t, rows()...)
	chord(m, "gi")
	if v, _ := m.gtdView(); v.ID != "inbox" {
		t.Fatalf("g i: view %d", m.view)
	}
	for keys, want := range map[string]string{"gf": "focus", "go": "outbox", "gn": "next", "gw": "waiting", "gp": "projects"} {
		chord(m, keys)
		if v, _ := m.gtdView(); v.ID != want {
			t.Errorf("%s: view %d, want %s", keys, m.view, want)
		}
	}
	chord(m, "gg")
	if m.view != 0 || m.views[0].ID != "needs-me" {
		t.Fatalf("g g: view %d", m.view)
	}
	chord(m, "gz")
	if m.view != 0 || m.status != "g z: no such key" {
		t.Fatalf("g z: view %d, status %q", m.view, m.status)
	}
	if press(m, Key{Rune: 'V'}); m.prompt == nil || m.prompt.action != "review" {
		t.Fatalf("the key after a cancelled chord is not a key again: %+v", m.prompt)
	}
}

func TestDigitsDoNotSwitchViews(t *testing.T) {
	m := gtdModel(t, rows()...)
	for _, k := range []rune{'1', '2', '3'} {
		press(m, Key{Rune: k})
		if v, ok := m.gtdView(); !ok || v.ID != "focus" {
			t.Fatalf("%c moved to view %d", k, m.view)
		}
	}
	asks := asking(api.QuestionView{State: "asked", Kind: "choice", Text: "Publish?",
		Options: []record.Option{{ID: "a", Label: "Comment"}, {ID: "b", Label: "Approve"}, {ID: "c", Label: "Hold"}}})
	m.SetData(append([]api.Row{asks}, rows()...), api.Health{})
	chord(m, "gg")
	press(m, Key{Name: KeyUp})
	for _, k := range []rune{'1', '2', '3'} {
		if c, ok := press(m, Key{Rune: k}); ok || m.prompt == nil || !m.prompt.confirm {
			t.Fatalf("%c on a question in Needs me: sent %+v %v, prompt %+v, status %q, sel %v", k, c, ok, m.prompt, m.status, m.sel)
		}
		if c, ok := press(m, Key{Rune: 'n'}); ok {
			t.Fatalf("a key other than y sent %+v", c)
		}
	}
	press(m, Key{Name: KeyDown})
	for _, k := range []rune{'1', '2', '3'} {
		press(m, Key{Rune: k})
		if m.view != 0 || m.prompt != nil {
			t.Fatalf("%c moved from Needs me to view %d", k, m.view)
		}
	}
	var seen []string
	for range 4 {
		seen = append(seen, m.viewKey())
		press(m, Key{Name: KeyTab})
	}
	if want := []string{"rows:needs-me", "rows:mine", "rows:loop", "plan"}; !slices.Equal(seen, want) {
		t.Fatalf("g g then Tab: %v", seen)
	}
	if m.view != 0 {
		t.Fatalf("Tab leaves the group for view %d", m.view)
	}
	if help := m.help(); strings.Contains(help, "1-4") || !strings.Contains(help, "g view") {
		t.Fatalf("help %q", help)
	}
}

func TestVerbKeysSendEntryVerbs(t *testing.T) {
	for _, c := range []struct {
		key   Key
		verb  string
		typed string
		args  map[string]string
	}{
		{Key{Rune: 'x'}, "next", "", nil},
		{Key{Rune: 'w'}, "waiting", "ana", map[string]string{"on": "ana"}},
		{Key{Rune: 's'}, "schedule", "2026-10-30", map[string]string{"until": "2026-10-30"}},
		{Key{Rune: 'y'}, "someday", "", nil},
		{Key{Rune: 'l'}, "reference", "", nil},
		{Key{Rune: 'f'}, "focus", "", nil},
		{Key{Rune: 'p'}, "project", "Release 2", map[string]string{"title": "Release 2"}},
		{Key{Rune: 'a'}, "approve", "", nil},
		{Key{Rune: 'd'}, "delegate", "", nil},
		{Key{Rune: 'd'}, "delegate", "keep it short", map[string]string{"guidance": "keep it short"}},
		{Key{Rune: 'r'}, "reply", "On it", map[string]string{"text": "On it"}},
		{Key{Rune: 'e'}, "done", "", nil},
		{Key{Name: KeyBackspace}, "dismiss", "", nil},
	} {
		t.Run(c.verb, func(t *testing.T) {
			m := gtdModel(t)
			got, ok := press(m, c.key)
			if _, asks := verbAsks[c.verb]; asks || c.verb == "delegate" || c.verb == "reply" {
				if ok || m.prompt == nil {
					t.Fatalf("%s sent before asking: %+v", c.verb, got)
				}
				got, ok = press(m, append(typed(c.typed), Key{Name: KeyEnter})...)
			}
			if !ok || got.entry != "e1" || got.verb != c.verb || !reflect.DeepEqual(got.args, c.args) {
				t.Fatalf("%+v %v", got, ok)
			}
			d := &fakeDaemon{}
			if _, err := carry(context.Background(), Config{Daemon: d}, got); err != nil {
				t.Fatal(err)
			}
			if want := []entryDo{{"e1", c.verb, "tui", c.args}}; !reflect.DeepEqual(d.dos(), want) {
				t.Fatalf("sent %+v", d.dos())
			}
		})
	}
	held := row(7, "Batch export", "round_pending", "needs-me")
	held.Actions = []api.ActionState{{ID: "run_now", Enabled: true, Confirm: "Run the held round on acme/api#7 now? It does not count against the cap."}}
	m := gtdModel(t, held)
	if _, ok := press(m, Key{Rune: 'a'}); ok || m.prompt == nil || !m.prompt.confirm || !strings.HasPrefix(m.prompt.label, "Run the held round") {
		t.Fatalf("a bridged to a confirmed run_now did not ask: %+v", m.prompt)
	}
	if c, ok := press(m, Key{Rune: 'y'}); !ok || c.verb != "approve" || c.entry != "e1" {
		t.Fatalf("a then y: %+v %v", c, ok)
	}

	m = gtdModel(t)
	chord(m, "gi")
	if _, ok := press(m, Key{Rune: 'a'}); ok || m.status != "approve: approve is not offered here" {
		t.Fatalf("a disabled verb: status %q", m.status)
	}
	press(m, Key{Rune: 's'}, Key{Name: KeyEnter})
	if !strings.Contains(m.status, "cancelled") {
		t.Fatalf("an empty schedule: status %q", m.status)
	}
}

func TestCaptureAndOpenSourceInTUI(t *testing.T) {
	r := row(7, "Batch export", "review", "needs-me")
	r.Link = "https://github.com/acme/api/pull/7"
	m := gtdModel(t, r)
	c, ok := press(m, append(append([]Key{{Rune: 'n'}}, typed("Buy cables")...), Key{Name: KeyEnter})...)
	if !ok || c.capture != "Buy cables" {
		t.Fatalf("capture %+v %v", c, ok)
	}
	d := &fakeDaemon{entries: map[string][]api.EntryView{}}
	if got, err := carry(context.Background(), Config{Daemon: d}, c); err != nil || !slices.Equal(d.captures, []string{"Buy cables"}) || len(got.lines) != 1 {
		t.Fatalf("captured %v: %v", d.captures, err)
	}

	var opened []string
	cfg := Config{Daemon: d, OpenURL: func(u string) error { opened = append(opened, u); return nil }}
	c, ok = press(m, Key{Rune: 'o'})
	if !ok || c.open != r.Link {
		t.Fatalf("o: %+v %v", c, ok)
	}
	if _, err := carry(context.Background(), cfg, c); err != nil || !slices.Equal(opened, []string{r.Link}) {
		t.Fatalf("opened %v: %v", opened, err)
	}

	slack := unit.Unit{Provider: "slack", Key: "C1/171.1"}
	e := entry("e20", "Thread with no row", "next")
	e.Units = []unit.Unit{slack}
	e.Cards = []api.CardView{{Card: card.Card{Unit: slack, Link: "https://chat.example/C1/171.1"}}}
	d.entries["focus"] = []api.EntryView{e}
	listed := e
	listed.Cards = nil
	m.SetEntries("focus", []api.EntryView{listed}, gtdCounters())
	m.Done(c, nil, nil)
	c, ok = press(m, Key{Rune: 'o'})
	if !ok || !c.openFind {
		t.Fatalf("o without a row: %+v %v", c, ok)
	}
	if _, err := carry(context.Background(), cfg, c); err != nil || opened[1] != "https://chat.example/C1/171.1" {
		t.Fatalf("opened %v: %v", opened, err)
	}
}

func TestOnlyHTTPSLinksOpen(t *testing.T) {
	var opened []string
	cfg := Config{Daemon: &fakeDaemon{}, OpenURL: func(u string) error { opened = append(opened, u); return nil }}
	for _, link := range []string{"file:///etc/passwd", "-a Calculator", "vscode://x", "http://example.com"} {
		if _, err := carry(context.Background(), cfg, Command{Action: "open-source", open: link}); err == nil || !strings.Contains(err.Error(), "only https") {
			t.Errorf("%s opened", link)
		}
	}
	if len(opened) != 0 {
		t.Fatalf("opened %v", opened)
	}
}

func TestDigitsAnswerTheQuestion(t *testing.T) {
	m := gtdModel(t)
	chord(m, "gi")
	if _, ok := press(m, Key{Rune: '2'}); ok {
		t.Fatal("a digit answered with the detail closed")
	}
	c, ok := press(m, Key{Name: KeyEnter})
	if !ok || !c.fetch || c.entry != "e2" {
		t.Fatalf("Enter: %+v %v", c, ok)
	}
	e := gtdEntries()["inbox"][0]
	m.SetDetail("e2", e, nil)
	screen := screenOf(m, 120, 40)
	for _, want := range []string{"Which window?", "1. Before six", "2. After ten (recommended)"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("screen lacks %q:\n%s", want, screen)
		}
	}
	c, ok = press(m, Key{Rune: '2'})
	if want := map[string]string{"option_id": "late", "guidance": "After ten (option late)"}; !ok || c.verb != "answer" || c.entry != "e2" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("2: %+v %v", c, ok)
	}

	m = newModel(t)
	m.SetData([]api.Row{asking(api.QuestionView{State: "asked", Kind: "choice", Text: "Which fix?",
		Options: []record.Option{{ID: "a", Label: "Patch"}, {ID: "b", Label: "Revert"}}})}, api.Health{})
	if c, ok := press(m, Key{Rune: '1'}); ok || m.prompt == nil || m.prompt.label != "answer acme/api#5: 1. Patch?" {
		t.Fatalf("1 on a row's question did not ask: %+v %v %+v", c, ok, m.prompt)
	}
	c, ok = press(m, Key{Rune: 'y'})
	if want := (api.ActionRequest{Guidance: "Patch (option a)", OptionID: "a", Source: "tui"}); !ok || c.Action != "answer" || !reflect.DeepEqual(c.Req, want) {
		t.Fatalf("1 then y on a row's question: %+v %v", c, ok)
	}

	m = gtdModel(t)
	chord(m, "gi")
	reads++
	if c, ok := m.Key(Key{Name: KeyEnter, Read: reads}); !ok || !c.fetch {
		t.Fatalf("Enter: %+v", c)
	}
	m.SetDetail("e2", gtdEntries()["inbox"][0], nil)
	if c, ok := m.Key(Key{Rune: '2', Read: reads}); ok {
		t.Fatalf("a digit typed ahead with the Enter answered: %+v", c)
	}
}

func TestPluginKeysRunUnitActions(t *testing.T) {
	m := gtdModel(t, row(7, "Batch export", "review", "needs-me"))
	c, ok := press(m, Key{Rune: 'H'})
	if !ok || c.Action != "pause" || c.Unit != ref(7).Unit() || c.Req.Source != "tui" {
		t.Fatalf("H: %+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	if _, ok := press(m, Key{Rune: 'N'}); ok || m.status != "no nudge on Reply on the batch export review" {
		t.Fatalf("N the row does not offer: status %q", m.status)
	}
}

func TestPlanViewReachableAndKeys(t *testing.T) {
	m := gtdModel(t, oneRow()...)
	m.SetPlanSpecs(servedSpecs(t).Plan)
	m.SetPlan(planItems(), nil)
	chord(m, "gg")
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	if m.view != m.planView() {
		t.Fatalf("g g and three tabs: view %d", m.view)
	}
	c, ok := press(m, Key{Name: KeyEnter})
	if !ok || c.Action != "plan_apply" || c.PlanID == "" {
		t.Fatalf("Enter: %+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	press(m, Key{Name: KeyDown})
	c, ok = press(m, Key{Rune: 'X'})
	if !ok || c.Action != "plan_dismiss" || c.PlanID == "" {
		t.Fatalf("X: %+v %v", c, ok)
	}
}

func TestPaletteAndHelp(t *testing.T) {
	m := gtdModel(t, row(7, "Batch export", "review", "needs-me"))
	press(m, Key{Name: KeyCtrlK})
	if m.overlay == nil {
		t.Fatal("no palette")
	}
	screen := squash(screenOf(m, 120, 60))
	for _, want := range []string{"Actions", "x Next", "Backspace Dismiss", "H Pause acme/api#7", "V Review acme/api#7", "o open the source", "n capture", "Area..."} {
		if !strings.Contains(screen, want) {
			t.Errorf("palette lacks %q:\n%s", want, screen)
		}
	}
	for range m.overlay.items {
		press(m, Key{Name: KeyDown})
	}
	press(m, Key{Name: KeyEnter})
	if m.overlay == nil || m.overlay.title != "Area" {
		t.Fatalf("Area... opened %+v", m.overlay)
	}
	press(m, Key{Name: KeyDown}, Key{Name: KeyDown}, Key{Name: KeyEnter})
	if m.area != "home" || m.overlay != nil {
		t.Fatalf("area %q", m.area)
	}
	if _, area, _ := m.EntryQuery(); area != "home" {
		t.Fatalf("the entries do not follow the area: %q", area)
	}

	press(m, Key{Rune: '?'})
	screen = squash(screenOf(m, 120, 80))
	for _, want := range []string{"Keys", "Everywhere", "g i Inbox", "On the selection", "x to Next", "In a question", "1 answer option 1"} {
		if !strings.Contains(screen, want) {
			t.Errorf("help lacks %q:\n%s", want, screen)
		}
	}
	press(m, Key{Rune: 'z'})
	if m.overlay != nil {
		t.Fatal("a key did not close the help")
	}
}

func TestShiftDigitsPickAreas(t *testing.T) {
	m := gtdModel(t)
	press(m, Key{Rune: '!'})
	if m.area != "work" {
		t.Fatalf("! picked %q", m.area)
	}
	press(m, Key{Rune: '@'})
	if m.area != "home" || !strings.Contains(m.Render(120, 20, now)[0], "area home") {
		t.Fatalf("@ picked %q", m.area)
	}
	press(m, Key{Rune: '#'})
	if m.area != "home" || m.status != "no area 3" {
		t.Fatalf("# with two areas: %q, status %q", m.area, m.status)
	}
}

func TestEntryDetail(t *testing.T) {
	r := row(7, "Batch export", "review", "needs-me")
	m := gtdModel(t, r)
	c, ok := press(m, Key{Name: KeyEnter})
	if !ok || !c.fetch || c.entry != "e1" || c.seen {
		t.Fatalf("Enter: %+v %v", c, ok)
	}
	e := gtdEntries()["focus"][0]
	e.Items = []api.ItemView{{Unit: ref(7).Unit(), ID: "i1", Summary: "answer the reviewer on the batch size", State: "open", Kind: "request"}}
	e.Cards = []api.CardView{{Card: card.Card{Unit: ref(7).Unit(), Title: "Batch export", State: "open", Why: "review requested",
		Signals: []card.Signal{{Key: "checks", Value: "passing"}}, LastHuman: &card.Human{Who: items.Person{Name: "ana"}, What: "comment"}}}}
	m.SetDetail("e1", e, nil)
	screen := screenOf(m, 120, 40)
	for _, want := range []string{"Reply on the batch export review", "state", "next · focus", "@computer", "request", "answer the reviewer on the batch size · open",
		"unit", "acme/api#7 Batch export", "review requested", "checks passing", "last: ana comment", "x next"} {
		if !strings.Contains(strings.ToLower(screen), strings.ToLower(want)) {
			t.Errorf("detail lacks %q:\n%s", want, screen)
		}
	}
	press(m, Key{Name: KeyEsc})
	if m.detailID != "" || strings.Contains(screenOf(m, 120, 40), "answer the reviewer") {
		t.Fatal("Esc left the detail open")
	}
}

func TestOpeningSendsSeen(t *testing.T) {
	m := gtdModel(t)
	chord(m, "gi")
	c, ok := press(m, Key{Name: KeyEnter})
	if !ok || !c.seen {
		t.Fatalf("Enter on a changed Inbox entry: %+v", c)
	}
	d := &fakeDaemon{entries: gtdEntries()}
	if got, err := carry(context.Background(), Config{Daemon: d}, c); err != nil || got.entry.ID != "e2" {
		t.Fatalf("%v %v", got.entry.ID, err)
	}
	if want := []entryDo{{"e2", "seen", "tui", nil}}; !reflect.DeepEqual(d.dos(), want) {
		t.Fatalf("sent %+v", d.dos())
	}
	press(m, Key{Name: KeyEsc})
	if c, _ := press(m, Key{Name: KeyEnter}); c.seen {
		t.Fatal("seen sent twice")
	}
	press(m, Key{Name: KeyEsc}, Key{Name: KeyDown})
	if c, _ := press(m, Key{Name: KeyEnter}); c.seen || c.entry != "e3" {
		t.Fatalf("an unchanged entry: %+v", c)
	}
}

func TestEntriesFollowChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow(), gtd: ptr(gtdCatalog()), entries: gtdEntries(), counters: gtdCounters()}
		term, done := start(t, d)
		eventually(t, "Focus", func() bool { return strings.Contains(term.screen(), "Reply on the batch export review") })
		eventually(t, "the stream", func() bool { return len(d.times(&d.eventsAt)) == 1 })
		before := len(d.asks())
		moved := gtdEntries()["focus"][0]
		moved.Title = "Reply on the batch export review, again"
		c := gtdCounters()
		c.Views["inbox"] = 5
		d.changeEntries("focus", []api.EntryView{moved}, c)
		eventually(t, "the delta and the counts", func() bool {
			s := term.screen()
			return strings.Contains(s, "review, again") && strings.Contains(s, "Inbox 5")
		})
		asks := d.asks()[before:]
		if len(asks) != 1 || asks[0].view != "focus" || asks[0].since != (api.Since{Epoch: "g1", Rev: 0}) {
			t.Fatalf("asked %+v after the change", asks)
		}
		term.press(Key{Rune: 'g'}, Key{Rune: 'i'})
		eventually(t, "the Inbox", func() bool { return strings.Contains(term.screen(), "Buy cables") })
		if asks := d.asks(); asks[len(asks)-1].view != "inbox" || asks[len(asks)-1].since != (api.Since{}) {
			t.Fatalf("the Inbox was asked %+v", asks[len(asks)-1])
		}
		quit(t, term, done)
	})
}

func (f *fakeDaemon) asks() []entryAsk {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.entryAsks)
}

func TestTheCatalogSurvivesAFailedPluginsRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registerBoard(t)
		d := &fakeDaemon{rows: oneRow(), plugins: boardPlugins(true), pluginsErr: errFake("plugins down")}
		term, done := start(t, d)
		eventually(t, "the catalog's views", func() bool {
			s := term.screen()
			return strings.Contains(s, "Needs me 1") && strings.Contains(s, "plugins down")
		})
		d.mu.Lock()
		d.pluginsErr = nil
		d.mu.Unlock()
		d.announce()
		eventually(t, "the plugins read again", func() bool { return strings.Contains(term.screen(), "Board") })
		d.mu.Lock()
		reads := d.catalogReads
		d.mu.Unlock()
		if reads != 1 {
			t.Fatalf("the catalog was read %d times", reads)
		}
		quit(t, term, done)
	})
}

// squash collapses the runs of spaces that align columns.
func squash(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

func TestOutboxRetrySendsTheRevOnScreen(t *testing.T) {
	m := gtdModel(t)
	e := gtdEntries()["outbox"][0]
	e.Draft = ptr(*e.Draft)
	e.Draft.State = "failed"
	e.Verbs = []api.VerbState{{Verb: "retry", Enabled: true}}
	m.SetEntries("outbox", []api.EntryView{e}, gtdCounters())
	chord(m, "go")
	if c, ok := m.entryVerb("retry", 0); ok || c.verb != "" {
		t.Fatalf("retried from the list: %+v", c)
	}
	press(m, Key{Name: KeyEnter})
	m.SetDetail(e.ID, e, nil)
	if _, ok := m.entryVerb("retry", 0); ok || m.prompt == nil || !m.prompt.confirm {
		t.Fatalf("retry did not ask: %+v", m.prompt)
	}
	c, ok := press(m, Key{Rune: 'y'})
	if want := map[string]string{"rev": "4"}; !ok || c.verb != "retry" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("retried %+v %v", c, ok)
	}
}

func TestTheFilterNarrowsTheGTDTabCounts(t *testing.T) {
	m := gtdModel(t)
	press(m, Key{Rune: '/'}, Key{Name: KeyPaste, Text: "computer"}, Key{Name: KeyEnter, Read: 1})
	tabs := m.Render(220, 20, now)[0]
	if strings.Contains(tabs, "Next 4") || !strings.Contains(tabs, "Next") {
		t.Fatalf("before the filtered counts arrive the tabs keep no unfiltered count: %q", tabs)
	}
	m.SetFilteredCounters("", api.Counters{Views: map[string]int{"focus": 1, "inbox": 0, "next": 2}})
	tabs = m.Render(220, 20, now)[0]
	for _, want := range []string{"[Focus 1]", "Inbox 0", "Next 2"} {
		if !strings.Contains(tabs, want) {
			t.Errorf("filtered tabs lack %q: %q", want, tabs)
		}
	}
	m.SetFilteredCounters("OSS", api.Counters{Views: map[string]int{"next": 9}})
	if tabs = m.Render(220, 20, now)[0]; !strings.Contains(tabs, "Next 2") {
		t.Errorf("counts of another area replaced the area's: %q", tabs)
	}
	press(m, Key{Name: KeyEsc, Read: 2})
	if tabs = m.Render(220, 20, now)[0]; !strings.Contains(tabs, "Next 4") || !strings.Contains(tabs, "Inbox 2") {
		t.Errorf("a cleared filter brings back the whole counts: %q", tabs)
	}
}

func TestTheFilterAsksTheDaemonForTheCountsItNarrows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow(), gtd: ptr(gtdCatalog()), entries: gtdEntries(), counters: gtdCounters()}
		term, done := start(t, d)
		eventually(t, "Focus", func() bool { return strings.Contains(term.screen(), "[Focus 1]") })
		term.press(Key{Rune: '/'}, Key{Name: KeyPaste, Text: "computer"})
		eventually(t, "the filtered counts", func() bool {
			s := term.screen()
			return strings.Contains(s, "Next 2") && strings.Contains(s, "Inbox 0") && strings.Contains(s, "Waiting 0")
		})
		d.mu.Lock()
		asks := slices.Clone(d.counterAsks)
		d.mu.Unlock()
		if len(asks) == 0 || !slices.Equal(asks[len(asks)-1], []string{"computer"}) {
			t.Errorf("counter asks %v", asks)
		}
		term.press(Key{Name: KeyEsc})
		quit(t, term, done)
	})
}
