package tui

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var now = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

// testRef is pull request n of acme/api.
type testRef int

func ref(n int) testRef { return testRef(n) }

func (r testRef) String() string { return fmt.Sprintf("acme/api#%d", int(r)) }
func (r testRef) Unit() unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: r.String()}
}

func row(n int, title string, kind api.Kind, views ...string) api.Row {
	r := api.Row{Unit: ref(n).Unit(), Label: ref(n).String(), Title: title, Kind: kind, Views: views, Summary: api.Summary{WaitsOn: "me", Reason: "review requested", Since: now.Add(-3 * time.Hour)},
		Facets: map[string][]string{"repo": {"acme/api"}, "author": {"ana"}}}
	setPR(&r, func(p *prView) { p.Author = "ana" })
	return offering(r, "run_now", "skip", "guide", "review", "address", "pause", "mute", "open")
}

func rows() []api.Row {
	return []api.Row{
		row(7, "Batch export", "review", "needs-me"),
		row(8, "Cache warmup", "changes", "needs-me", "mine"),
		row(9, "Loop one", "loop_working", "loop"),
	}
}

var reads uint64

// press sends keys as separate reads, as a person types them.
func press(m *Model, keys ...Key) (Command, bool) {
	var c Command
	var ok bool
	for _, k := range keys {
		reads++
		k.Read = reads
		c, ok = m.Key(k)
	}
	return c, ok
}

// together sends keys in one read, as a paste without bracketed paste does.
func together(m *Model, keys ...Key) (c Command, ok bool) {
	reads++
	for _, k := range keys {
		k.Read = reads
		if c, ok = m.Key(k); ok {
			return c, ok
		}
	}
	return c, ok
}

func typed(s string) []Key {
	var out []Key
	for _, r := range s {
		out = append(out, Key{Rune: r})
	}
	return out
}

func TestMoveAndSwitchViews(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	if r, _ := m.selected(); r.Unit != ref(7).Unit() {
		t.Fatalf("first selection %v", r.Unit)
	}
	press(m, Key{Name: KeyDown}, Key{Rune: 'j'})
	if r, _ := m.selected(); r.Unit != ref(8).Unit() {
		t.Fatalf("down past the end: %v", r.Unit)
	}
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab})
	if r, _ := m.selected(); r.Unit != ref(9).Unit() {
		t.Fatalf("in loop: %v", r.Unit)
	}
	press(m, Key{Name: KeyTab})
	if m.view != m.planView() {
		t.Fatalf("tab went to view %d", m.view)
	}
}

func TestSelectionFollowsThePullRequestAcrossRefreshes(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, Key{Name: KeyDown})
	m.SetData(append([]api.Row{row(5, "New one", "review", "needs-me")}, rows()...), api.Health{})
	if r, _ := m.selected(); r.Unit != ref(8).Unit() {
		t.Fatalf("selection moved to %v", r.Unit)
	}
}

func TestReviewAsksForGuidanceThenSends(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	if _, ok := press(m, Key{Rune: 'V'}); ok || m.prompt == nil {
		t.Fatal("V did not open the prompt")
	}
	keys := append(typed("only the migratiom"), Key{Name: KeyBackspace}, Key{Rune: 'n'}, Key{Name: KeyEnter})
	c, ok := press(m, keys...)
	if !ok || c.Action != "review" || c.Unit != ref(7).Unit() || c.Req.Guidance != "only the migration" {
		t.Fatalf("%v %+v", ok, c)
	}
	if m.prompt != nil || m.busy[ref(7).Unit()] != "review" {
		t.Fatalf("prompt %v busy %v", m.prompt, m.busy)
	}
	if _, ok := press(m, Key{Rune: 'O'}); ok {
		t.Fatal("a second action ran while the first was busy")
	}
	m.Done(c, []string{"new round in REVIEW feat/x"}, nil)
	if _, busy := m.busy[ref(7).Unit()]; busy || m.status != "review acme/api#7: new round in REVIEW feat/x" {
		t.Fatalf("busy %v status %q", m.busy, m.status)
	}
}

func TestEscCancelsThePrompt(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	if _, ok := press(m, append([]Key{{Rune: 'N'}}, append(typed("x"), Key{Name: KeyEsc})...)...); ok || m.prompt != nil {
		t.Fatal("esc sent the nudge")
	}
}

func TestMuteHoldSkipRunAndGuide(t *testing.T) {
	m := newModel(t)
	held := row(4, "Held", "held", "needs-me")
	pending := row(3, "Pending", "round_pending", "needs-me")
	pending.Pending = &api.PendingRound{Intent: "follow-up", Deadline: now.Add(40 * time.Second)}
	m.SetData([]api.Row{pending, held}, api.Health{})

	if c, ok := press(m, Key{Rune: 'R'}); !ok || c.Action != "run_now" || c.Unit != ref(3).Unit() {
		t.Fatalf("run the pending round: %+v", c)
	}
	m.Done(Command{Unit: ref(3).Unit()}, nil, nil)
	c, ok := press(m, append([]Key{{Rune: 'G'}}, append(typed("nits only"), Key{Name: KeyEnter})...)...)
	if !ok || c.Action != "guide" || c.Req.Guidance != "nits only" {
		t.Fatalf("guide %+v", c)
	}
	m.Done(c, nil, nil)
	if c, ok := press(m, Key{Rune: 'M'}, Key{Name: KeyEnter}); !ok || c.Action != "mute" || !c.Req.UntilState {
		t.Fatalf("mute until state %+v", c)
	}
	m.Done(Command{Unit: ref(3).Unit()}, nil, nil)
	press(m, Key{Name: KeyDown})
	if c, ok := press(m, Key{Rune: 'H'}); !ok || c.Action != "pause" {
		t.Fatalf("held row: %+v", c)
	}
	m.Done(Command{Unit: ref(4).Unit()}, nil, nil)
	if c, ok := press(m, Key{Rune: 'R'}); !ok || c.Action != "run_now" {
		t.Fatalf("run: %+v", c)
	}
	m.Done(Command{Unit: ref(4).Unit()}, nil, nil)
	if _, ok := press(m, Key{Rune: 'G'}); ok || m.prompt == nil {
		t.Fatalf("guide: prompt %v status %q", m.prompt, m.status)
	}
}

func asking(q api.QuestionView) api.Row {
	r := offering(row(5, "Asks", "loop_waiting", "needs-me"), "answer", "continue", "dismiss")
	r.Question = &q
	return r
}

func TestAnswerTypesTheReplyForAWaitingAgent(t *testing.T) {
	m := newModel(t)
	m.SetData([]api.Row{asking(api.QuestionView{State: "asked", Text: "Publish the review?"})}, api.Health{})
	if _, ok := press(m, Key{Rune: 'W'}, Key{Name: KeyEnter}); ok || !strings.Contains(m.status, "nothing typed") {
		t.Fatalf("empty answer sent: status %q", m.status)
	}
	c, ok := press(m, append([]Key{{Rune: 'W'}}, append(typed("sim, publica"), Key{Name: KeyEnter})...)...)
	want := api.ActionRequest{Guidance: "sim, publica", Source: "tui"}
	if !ok || c.Action != "answer" || c.Unit != ref(5).Unit() || !reflect.DeepEqual(c.Req, want) {
		t.Fatalf("answer %+v", c)
	}
}

func TestAnswerWithoutAQuestionIsRefused(t *testing.T) {
	m := newModel(t)
	r := row(5, "Asks", "loop_waiting", "needs-me")
	r.Actions = append(r.Actions, api.ActionState{ID: "answer", Reason: "no open question on acme/api#5"})
	m.SetData([]api.Row{r}, api.Health{})
	if _, ok := press(m, Key{Rune: 'W'}); ok || m.prompt != nil || m.status != "no open question on acme/api#5" {
		t.Fatalf("prompt %v status %q", m.prompt, m.status)
	}
}

func TestANumberAnswersAChoiceByItsOption(t *testing.T) {
	m := newModel(t)
	m.SetData([]api.Row{asking(api.QuestionView{State: "asked", Kind: "choice", Text: "Which fix?", Recommend: "b",
		Verdict: "REQUEST_CHANGES", Blocking: 2, NonBlocking: 1,
		Options: []record.Option{{ID: "a", Label: "Patch"}, {ID: "b", Label: "Revert"}}})}, api.Health{})
	press(m, Key{Rune: 'W'})
	if want := "acme/api#5: Which fix? (drafted REQUEST_CHANGES, 2 blocking, 1 non-blocking) 1. Patch  2. Revert (recommended)"; m.prompt == nil || m.prompt.label != want {
		t.Fatalf("label %+v, want %q", m.prompt, want)
	}
	c, ok := press(m, Key{Rune: '2'}, Key{Name: KeyEnter})
	want := api.ActionRequest{Guidance: "Revert (option b)", OptionID: "b", Source: "tui"}
	if !ok || c.Action != "answer" || !reflect.DeepEqual(c.Req, want) {
		t.Fatalf("answer %+v", c)
	}
	m.Done(c, nil, nil)
	c, ok = press(m, append([]Key{{Rune: 'W'}}, append(typed("3"), Key{Name: KeyEnter})...)...)
	if want := (api.ActionRequest{Guidance: "3", Source: "tui"}); !ok || !reflect.DeepEqual(c.Req, want) {
		t.Fatalf("a number past the options is a free answer: %+v", c)
	}
}

func TestYAndNAnswerAConfirm(t *testing.T) {
	for key, want := range map[string]api.ActionRequest{
		"y":       {Yes: true, Source: "tui"},
		"Y":       {Yes: true, Source: "tui"},
		"yes":     {Yes: true, Source: "tui"},
		"n":       {No: true, Source: "tui"},
		"No":      {No: true, Source: "tui"},
		"not yet": {Guidance: "not yet", Source: "tui"},
	} {
		m := newModel(t)
		m.SetData([]api.Row{asking(api.QuestionView{State: "asked", Kind: "confirm", Text: "Publish?"})}, api.Health{})
		c, ok := press(m, append([]Key{{Rune: 'W'}}, append(typed(key), Key{Name: KeyEnter})...)...)
		if !ok || c.Action != "answer" || !reflect.DeepEqual(c.Req, want) {
			t.Errorf("%s: %+v", key, c)
		}
	}
}

func TestContinueAndDismissTheTurn(t *testing.T) {
	for key, action := range map[rune]string{'C': "continue", 'X': "dismiss"} {
		m := newModel(t)
		m.SetData([]api.Row{asking(api.QuestionView{State: "failed"})}, api.Health{})
		if c, ok := press(m, Key{Rune: key}); !ok || c.Action != action || c.Unit != ref(5).Unit() {
			t.Errorf("%c: %+v", key, c)
		}
		m = newModel(t)
		working := row(5, "Working", "loop_working", "loop")
		working.Actions = append(working.Actions, api.ActionState{ID: action, Reason: "no open question on acme/api#5"})
		m.SetData([]api.Row{working}, api.Health{})
		press(m, Key{Name: KeyTab}, Key{Name: KeyTab})
		if _, ok := press(m, Key{Rune: key}); ok || m.status != "no open question on acme/api#5" {
			t.Errorf("%c without a question: status %q", key, m.status)
		}
	}
}

func TestFailureAndDaemonErrorReachTheStatusLine(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	m.Done(Command{Unit: ref(7).Unit(), Label: ref(7).String(), Action: "nudge"}, nil, errors.New("acme/api#7 waits on you"))
	if m.status != "nudge acme/api#7 failed: acme/api#7 waits on you" {
		t.Fatalf("status %q", m.status)
	}
	m.SetError(errors.New("connection refused"))
	if m.err == "" || len(m.rows) != 3 {
		t.Fatalf("err %q rows %d", m.err, len(m.rows))
	}
}

func TestQuit(t *testing.T) {
	for _, k := range []Key{{Rune: 'q'}, {Name: KeyCtrlC}} {
		m := newModel(t)
		press(m, k)
		if !m.Quit() {
			t.Errorf("%+v did not quit", k)
		}
	}
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, Key{Rune: 'V'}, Key{Rune: 'q'})
	if m.Quit() {
		t.Fatal("q typed into the prompt quit")
	}
}

func TestHardToUndoActionsAskForTheServedConfirm(t *testing.T) {
	m, r := modelOn(t, "others_not_adopted")
	var takeOver = "Take over acme/api#7? It becomes yours to develop: " + brand.Name + " creates or renames its author workspace."
	if _, ok := press(m, Key{Rune: 'A'}, Key{Rune: 'A'}); ok || m.prompt != nil || m.status != "take_over acme/api#7 cancelled" {
		t.Fatalf("a second A took over: prompt %v status %q", m.prompt, m.status)
	}
	press(m, Key{Rune: 'A'})
	if last := m.Render(160, 8, now)[7]; !strings.HasPrefix(last, takeOver+" y confirms") {
		t.Fatalf("confirm line %q", last)
	}
	if c, ok := press(m, Key{Rune: 'y'}); !ok || c.Action != "take_over" || c.Unit != r.Unit || c.Req.Source != "tui" {
		t.Fatalf("y did not take over: %+v", c)
	}
}

func TestKeysFollowTheServedActions(t *testing.T) {
	specs := servedSpecs(t).Specs
	for name, r := range fixtures(t) {
		r.Views = []string{"needs-me"}
		for _, spec := range specs {
			if spec.Key == "" || !slices.Contains(spec.Surfaces, "tui") {
				continue
			}
			m := newModel(t)
			m.SetData([]api.Row{r}, api.Health{})
			c, sent := press(m, Key{Rune: rune(spec.Key[0])})
			st, offered := actionState(r, spec.ID)
			where := name + "/" + spec.ID
			switch {
			case !offered:
				if siblingOffered(specs, spec, r) {
					continue
				}
				if sent || m.prompt != nil || m.status != "no "+strings.ToLower(firstWithKey(specs, spec.Key).Label)+" on "+r.Label {
					t.Errorf("%s absent: status %q prompt %v sent %v", where, m.status, m.prompt, sent)
				}
			case !st.Enabled:
				if sent || m.prompt != nil || m.status != st.Reason {
					t.Errorf("%s disabled: status %q, want %q", where, m.status, st.Reason)
				}
			case spec.Input != nil:
				if sent || m.prompt == nil || m.prompt.confirm {
					t.Errorf("%s enabled with an input: prompt %v", where, m.prompt)
					continue
				}
				c, sent = press(m, append(typed("x"), Key{Name: KeyEnter})...)
				if st.Confirm != "" {
					if sent || m.prompt == nil || !m.prompt.confirm || m.prompt.label != st.Confirm {
						t.Errorf("%s: Enter did not ask the served confirm: %+v", where, m.prompt)
					}
				} else if !sent || c.Action != spec.ID || c.Unit != r.Unit || c.Req.Source != "tui" {
					t.Errorf("%s: Enter sent %v %+v", where, sent, c)
				}
			case st.Confirm != "":
				if sent || m.prompt == nil || !m.prompt.confirm || m.prompt.label != st.Confirm {
					t.Errorf("%s confirm: prompt %+v, want %q", where, m.prompt, st.Confirm)
				}
				if c, sent = press(m, Key{Rune: 'y'}); !sent || c.Action != spec.ID || c.Req.Basis != st.Basis {
					t.Errorf("%s: y sent %v %+v", where, sent, c)
				}
			default:
				if !sent || c.Action != spec.ID || c.Unit != r.Unit || c.Req.Basis != st.Basis || c.Req.Source != "tui" {
					t.Errorf("%s enabled: sent %v %+v", where, sent, c)
				}
			}
		}
	}
}

func siblingOffered(specs []api.ActionSpec, spec api.ActionSpec, r api.Row) bool {
	for _, o := range specs {
		if _, ok := actionState(r, o.ID); ok && o.Key == spec.Key && o.ID != spec.ID {
			return true
		}
	}
	return false
}

func TestAnAbsentActionSaysSo(t *testing.T) {
	m, r := modelOn(t, "done_muted")
	if _, ok := press(m, Key{Rune: 'V'}); ok || m.status != "no review on "+r.Label {
		t.Fatalf("status %q", m.status)
	}
}

func TestTheServedConfirmFollowsTheInput(t *testing.T) {
	m, r := modelOn(t, "mine_no_reviewer")
	press(m, append([]Key{{Rune: 'N'}}, append(typed("the docs"), Key{Name: KeyEnter})...)...)
	st, _ := actionState(r, "nudge")
	if m.prompt == nil || !m.prompt.confirm || m.prompt.label != st.Confirm {
		t.Fatalf("prompt %+v, want the confirm %q", m.prompt, st.Confirm)
	}
	if c, ok := press(m, Key{Rune: 'y'}); !ok || c.Action != "nudge" || c.Req.Guidance != "the docs" || c.Req.Source != "tui" {
		t.Fatalf("nudge %+v", c)
	}
}

func TestAGuidanceBoxStopsAtTheServedMax(t *testing.T) {
	m, _ := modelOn(t, "mine_no_reviewer")
	press(m, Key{Rune: 'N'}, Key{Name: KeyPaste, Text: strings.Repeat("x", 800)})
	if n := len(m.prompt.text); n != 500 {
		t.Fatalf("%d runes typed, nudge allows 500", n)
	}
}

func TestARequiredInputRefusesNothing(t *testing.T) {
	m, _ := modelOn(t, "pending")
	if _, ok := press(m, Key{Rune: 'G'}, Key{Name: KeyEnter}); ok || !strings.Contains(m.status, "nothing typed") {
		t.Fatalf("status %q", m.status)
	}
}

func TestHelpLineListsTheRowsEnabledActions(t *testing.T) {
	m, _ := modelOn(t, "pending")
	help := m.help()
	for _, want := range []string{"R run now", "S skip", "G guide", "V review", "A take over", "H pause", "M mute", "O open"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q: %s", want, help)
		}
	}
	for _, not := range []string{"F address", "N nudge", "W answer", "C continue", "X dismiss", "resume", "unmute"} {
		if strings.Contains(help, not) {
			t.Errorf("help offers %q the row does not: %s", not, help)
		}
	}
}

func TestToggleKeysPickTheOfferedSide(t *testing.T) {
	for _, tc := range []struct{ fixture, key, want string }{
		{"pending", "H", "pause"},
		{"user_hold", "H", "resume"},
		{"pending", "M", "mute"},
		{"done_muted", "M", "unmute"},
	} {
		m, _ := modelOn(t, tc.fixture)
		press(m, Key{Rune: rune(tc.key[0])})
		got := ""
		switch {
		case m.prompt != nil:
			got = m.prompt.action
		default:
			for _, a := range m.busy {
				got = a
			}
		}
		if got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.fixture, tc.key, got, tc.want)
		}
	}
}

func TestMuteDefaultsToUntilTheRowChanges(t *testing.T) {
	m, _ := modelOn(t, "pending")
	if c, ok := press(m, Key{Rune: 'M'}, Key{Name: KeyEnter}); !ok || c.Action != "mute" || !c.Req.UntilState || c.Req.Until != "" {
		t.Fatalf("%+v", c)
	}
}

func TestSelectionKeepsItsPlaceWhenTheRowLeaves(t *testing.T) {
	m := newModel(t)
	m.SetData([]api.Row{row(7, "a", "review", "needs-me"), row(8, "b", "review", "needs-me"), row(9, "c", "review", "needs-me")}, api.Health{})
	press(m, Key{Name: KeyDown})
	m.SetData([]api.Row{row(7, "a", "review", "needs-me"), row(9, "c", "review", "needs-me")}, api.Health{})
	if r, _ := m.selected(); r.Unit != ref(9).Unit() {
		t.Fatalf("selection went to %v", r.Unit)
	}
	m.SetData([]api.Row{row(7, "a", "review", "needs-me")}, api.Health{})
	if r, _ := m.selected(); r.Unit != ref(7).Unit() {
		t.Fatalf("selection past the end: %v", r.Unit)
	}
}

func TestPasteIsTextOnlyInAPrompt(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	if _, ok := press(m, Key{Name: KeyPaste, Text: "nA\ry"}); ok || m.prompt != nil || m.quit {
		t.Fatal("a paste outside a prompt acted")
	}
	press(m, Key{Rune: 'V'}, Key{Name: KeyPaste, Text: "keep\nthe \u202eold\u009b column"}, Key{Rune: '\u200f'})
	if got := string(m.prompt.text); got != "keep the old column" {
		t.Fatalf("prompt text %q", got)
	}
}

func TestAnAnswerTypedAheadDoesNotCount(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	if _, ok := together(m, Key{Rune: 'A'}, Key{Rune: 'y'}); ok || m.prompt != nil {
		t.Fatalf("A and y in one read adopted: prompt %+v", m.prompt)
	}
	if _, ok := press(m, Key{Rune: 'A'}, Key{Name: KeyPaste, Text: "y"}); ok || m.prompt != nil {
		t.Fatal("a pasted y confirmed")
	}
	if _, ok := together(m, append([]Key{{Rune: 'V'}}, append(typed("go"), Key{Name: KeyEnter})...)...); ok || m.prompt == nil || string(m.prompt.text) != "go" {
		t.Fatalf("an Enter in the read that opened the prompt sent it: %+v", m.prompt)
	}
	if c, ok := press(m, Key{Name: KeyEnter}); !ok || c.Req.Guidance != "go" {
		t.Fatalf("a later Enter did not send: %+v", c)
	}
}

func TestAPromptHoldsAtMostAPasteOfText(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, Key{Rune: 'V'}, Key{Name: KeyPaste, Text: strings.Repeat("ab", pasteMax)}, Key{Rune: 'c'})
	if n := len(m.prompt.text); n != pasteMax {
		t.Fatalf("%d runes", n)
	}
	best := time.Duration(math.MaxInt64)
	for range 5 {
		start := time.Now()
		m.Render(80, 10, now)
		best = min(best, time.Since(start))
	}
	if !race && best > 200*time.Millisecond {
		t.Fatalf("the fastest of 5 renders of a full prompt took %v", best)
	}
}

func TestThePromptKeepsJoiners(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, Key{Rune: 'V'}, Key{Name: KeyPaste, Text: "👩\u200d💻 می\u200cخواهم"})
	if got := string(m.prompt.text); got != "👩\u200d💻 می\u200cخواهم" {
		t.Fatalf("%q", got)
	}
}

func TestADisabledNudgeShowsItsReason(t *testing.T) {
	m, r := modelOn(t, "nudged_recently")
	st, _ := actionState(r, "nudge")
	if _, ok := press(m, Key{Rune: 'N'}); ok || m.prompt != nil || m.status != st.Reason || st.Reason == "" {
		t.Fatalf("prompt %v status %q, want %q", m.prompt, m.status, st.Reason)
	}
}

func filterBy(m *Model, text string) {
	press(m, append([]Key{{Rune: '/'}}, typed(text)...)...)
}

func TestSlashFiltersTheViewAcrossFields(t *testing.T) {
	m := newModel(t)
	rs := rows()
	rs[0].Title = "Résumé export in batches"
	setPR(&rs[1], func(p *prView) {
		p.Head = "carol/cache-warmup"
		p.Brief = &api.BriefSummary{Size: "s", Risk: "low", Goal: "Retry the webhook delivery"}
	})
	setPR(&rs[0], func(p *prView) { p.Workspace = api.Workspace{DisplayName: "batch-ledger", Comment: "waiting on infra"} })
	m.SetData(rs, api.Health{})
	for text, want := range map[string]int{"webhook": 8, "carol": 8, "resume export": 7, "acme/api#8": 8, "RÉSUMÉ": 7, "batch-ledger": 7, "infra": 7} {
		filterBy(m, text)
		if got := m.visible(); len(got) != 1 || got[0].Unit != ref(want).Unit() {
			t.Errorf("filter %q: %v", text, got)
		}
		if r, _ := m.selected(); r.Unit != ref(want).Unit() {
			t.Errorf("filter %q selects %v", text, r.Unit)
		}
		press(m, Key{Name: KeyEsc})
	}
}

func TestKeysWhileFilteringAreText(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, Key{Rune: '/'})
	if _, ok := press(m, typed("rqA")...); ok || m.prompt != nil || m.Quit() {
		t.Fatal("a key typed into the filter ran an action")
	}
	press(m, Key{Name: KeyEnter})
	if m.filtering || string(m.filter) != "rqA" || len(m.visible()) != 0 {
		t.Fatalf("enter keeps the filter: %q %v", string(m.filter), m.visible())
	}
	view, status := m.view, m.status
	if _, ok := press(m, Key{Name: KeyPaste, Text: "x\x1b[2Jy"}); ok || string(m.filter) != "rqA" || m.filtering || m.prompt != nil || m.view != view || m.status != status {
		t.Fatalf("a paste outside the filter did something: filter %q filtering %v prompt %v view %d status %q", string(m.filter), m.filtering, m.prompt, m.view, m.status)
	}
	press(m, Key{Name: KeyEsc})
	if len(m.filter) != 0 || len(m.visible()) != 2 {
		t.Fatalf("esc clears the filter: %q %v", string(m.filter), m.visible())
	}
	press(m, Key{Rune: '/'}, Key{Name: KeyPaste, Text: "ca\x1b[2Jche\u202e"})
	if string(m.filter) != "ca [2Jche" {
		t.Fatalf("pasted control characters reached the filter: %q", string(m.filter))
	}
}

func TestPastingAPullRequestURLSelectsIt(t *testing.T) {
	m := newModel(t)
	rs := rows()
	for i := range rs {
		rs[i].Link = "https://github.com/" + strings.Replace(rs[i].Label, "#", "/pull/", 1)
	}
	m.SetData(rs, api.Health{})
	press(m, Key{Rune: '/'}, Key{Name: KeyPaste, Text: "https://github.com/acme/api/pull/9/files"}, Key{Name: KeyEnter})
	if m.views[m.view].ID != api.ViewLoop || m.sel != ref(9).Unit() || len(m.filter) != 0 {
		t.Fatalf("view %s, selection %v, filter %q", m.views[m.view].ID, m.sel, string(m.filter))
	}
	press(m, Key{Rune: '/'}, Key{Name: KeyPaste, Text: "https://github.com/acme/api/pull/99"}, Key{Name: KeyEnter})
	if !strings.Contains(m.status, "https://github.com/acme/api/pull/99 is not in") {
		t.Fatalf("status %q", m.status)
	}
}

func TestKeysTypedWithTheFilterInOneReadAreNotActions(t *testing.T) {
	for _, in := range []string{"/x\nh", "/x\x1b\x1bh", "/cache\nho"} {
		m := newModel(t)
		m.SetData(rows(), api.Health{})
		keys := Decode([]byte(in))
		if c, ok := together(m, keys...); ok || m.prompt != nil {
			t.Errorf("%q sent %+v", in, c)
		}
		if c, ok := press(m, Key{Rune: 'H'}); !ok || c.Action != "pause" && c.Action != "resume" {
			t.Errorf("%q: a later h does nothing: %+v", in, c)
		}
	}
}

func TestTheFilterSearchesTheLabelURLAndDecomposedAccents(t *testing.T) {
	m := newModel(t)
	rs := rows()
	rs[0].Title = "Cafe\u0301 export"
	rs[1].Link = "https://github.com/acme/api/pull/8"
	m.SetData(rs, api.Health{})
	for text, want := range map[string]int{"café": 7, "changes asked": 8, "github.com/acme/api/pull/8": 8} {
		filterBy(m, text)
		if got := m.visible(); len(got) != 1 || got[0].Unit != ref(want).Unit() {
			t.Errorf("filter %q: %v", text, got)
		}
		press(m, Key{Name: KeyEsc})
	}
}

func TestTheFilterKeepsAndRestoresTheSelection(t *testing.T) {
	m := newModel(t)
	rs := rows()
	rs[1].Title = "Cache warmup"
	m.SetData(rs, api.Health{})
	press(m, Key{Name: KeyDown})
	press(m, append([]Key{{Rune: '/'}}, typed("ca")...)...)
	if r, _ := m.selected(); r.Unit != ref(8).Unit() {
		t.Fatalf("the selection left a row that still matches: %v", r.Unit)
	}
	press(m, typed("x")...)
	press(m, Key{Name: KeyBackspace})
	if r, _ := m.selected(); r.Unit != ref(8).Unit() {
		t.Fatalf("the selection did not come back when its row matched again: %v", r.Unit)
	}
	press(m, typed("zzz")...)
	press(m, Key{Name: KeyEsc})
	if r, _ := m.selected(); r.Unit != ref(8).Unit() {
		t.Fatalf("esc did not restore the selection: %v", r.Unit)
	}
	press(m, Key{Rune: '/'}, Key{Name: KeyPaste, Text: "cache\nwarm\tup"})
	if string(m.filter) != "cache warm up" {
		t.Fatalf("paste %q", string(m.filter))
	}
}

func firstWithKey(specs []api.ActionSpec, key string) api.ActionSpec {
	return specs[slices.IndexFunc(specs, func(s api.ActionSpec) bool { return s.Key == key && slices.Contains(s.Surfaces, "tui") })]
}

func TestSelectionKeysRowsByTheWholeUnit(t *testing.T) {
	a := row(7, "Pull request", "review", "needs-me")
	b := row(8, "Session", "review", "needs-me")
	b.Unit = unit.Unit{Provider: unit.Session, Key: a.Unit.Key}
	m := modelWith(t, a, b)
	m.move(1)
	m.SetData([]api.Row{a, b}, api.Health{})
	if r, _ := m.selected(); r.Unit != b.Unit {
		t.Fatalf("selection fell back to %v", r.Unit)
	}
}
