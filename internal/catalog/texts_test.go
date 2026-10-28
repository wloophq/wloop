package catalog

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestEverySpecHasASummary(t *testing.T) {
	l := bundled(t)
	for _, s := range append(Specs(l), PlanSpecs(l)...) {
		if s.Label == "" || s.Summary == "" {
			t.Errorf("%s: label %q, summary %q", s.ID, s.Label, s.Summary)
		}
	}
}

func stateOf(t *testing.T, f Facts, id string) api.ActionState {
	t.Helper()
	for _, st := range States(f) {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("%s is not offered", id)
	return api.ActionState{}
}

// TestBuiltinTextsComeFromTheProfile pins every built-in text the row
// offers to the string Go wrote before actions.toml held it.
func TestBuiltinTextsComeFromTheProfile(t *testing.T) {
	l := bundled(t)
	base := func() Facts {
		return Facts{Intents: l, PR: r7, Owner: "acme", Repo: "api", Row: api.Row{Unit: r7}, Open: true, Tracked: true, GateOn: true, BriefOn: true, NudgeWho: []string{}}
	}
	slug := "acme/api#7"
	cases := []struct {
		name string
		id   string
		set  func(f *Facts)
		want api.ActionState
	}{
		{"held run_now", "run_now", func(f *Facts) { f.HeldByCap = true },
			api.ActionState{Confirm: "Run the held round on " + slug + " now? It does not count against the cap."}},
		{"pending run_now", "run_now", func(f *Facts) { f.HeldByCap, f.Row.Pending = true, &api.PendingRound{} }, api.ActionState{}},
		{"guide without a pending round", "guide", func(*Facts) {}, api.ActionState{Note: "Kept for the next automatic round"}},
		{"guide on a pending round", "guide", func(f *Facts) { f.Row.Pending = &api.PendingRound{} }, api.ActionState{}},
		{"guide with the gate off", "guide", func(f *Facts) { f.GateOn = false }, api.ActionState{Reason: "the daemon plans no automatic round; see doctor"}},
		{"trust_run", "trust_run", func(f *Facts) {
			f.StartBlocked, f.TrustFolder, f.TrustHomes = true, "/src/api", []string{"~/.claude", "~/.codex"}
		}, api.ActionState{Note: "Trust /src/api in ~/.claude, ~/.codex, then run the round"}},
		{"unblock", "unblock", func(f *Facts) { f.StartBlocked = true }, api.ActionState{Note: "Lift the block without trusting; the round waits for a new trigger"}},
		{"answer without a question", "answer", func(*Facts) {}, api.ActionState{Reason: "no open question on " + slug}},
		{"continue without a question", "continue", func(*Facts) {}, api.ActionState{Reason: "no open question on " + slug}},
		{"dismiss without a question", "dismiss", func(*Facts) {}, api.ActionState{Reason: "no open question on " + slug}},
		{"dismiss on a question", "dismiss", func(f *Facts) { f.Question = true },
			api.ActionState{Note: "Close the question; the pull request goes back to whoever it waits on"}},
		{"take_over of mine", "take_over", func(f *Facts) { f.Mine = true }, api.ActionState{Reason: "It is yours already"}},
		{"take_over", "take_over", func(*Facts) {},
			api.ActionState{Confirm: "Take over " + slug + "? It becomes yours to develop: " + brand.Name + " creates or renames its author workspace."}},
		{"remove on an open pull request", "remove", func(f *Facts) { f.Workspace.ID = "w" },
			api.ActionState{Confirm: "Remove the workspace of " + slug + "? Its checkout is deleted and its terminals are closed. It is refused while its agent works or waits, or when it holds uncommitted files, unpushed commits or ignored files outside build and dependency folders. The pull request is still open: no automatic first round brings the workspace back; Review or Address does."}},
		{"remove on a closed pull request", "remove", func(f *Facts) { f.Workspace.ID, f.Open = "w", false },
			api.ActionState{Confirm: "Remove the workspace of " + slug + "? Its checkout is deleted and its terminals are closed. It is refused while its agent works or waits, or when it holds uncommitted files, unpushed commits or ignored files outside build and dependency folders."}},
		{"remove_force", "remove_force", func(f *Facts) { f.Workspace.ID, f.Forceable = "w", "2 uncommitted files" },
			api.ActionState{Note: "Refused: 2 uncommitted files", Confirm: "Force remove the workspace of " + slug + "?\n\nIt was refused:\n2 uncommitted files\n\nForcing deletes its checkout with what is named above, for good."}},
		{"brief_refresh", "brief_refresh", func(*Facts) {}, api.ActionState{Note: "Make this pull request's brief again now"}},
		{"brief_refresh with the brief off", "brief_refresh", func(f *Facts) { f.BriefOn = false },
			api.ActionState{Reason: "the brief is off; set brief.enabled in the config"}},
		{"intake_without of a session", "intake_without", func(f *Facts) {
			f.Intakes = []Intake{{Intake: api.Intake{Seq: 42, State: api.IntakeAccepted}, FromSession: true}}
		}, api.ActionState{Reason: SessionWithout(42)}},
	}
	for _, c := range cases {
		f := base()
		c.set(&f)
		got := stateOf(t, f, c.id)
		if got.Note != c.want.Note || got.Confirm != c.want.Confirm || got.Reason != c.want.Reason || got.Warning != "" {
			t.Errorf("%s:\n got note %q confirm %q reason %q warning %q\nwant note %q confirm %q reason %q", c.name,
				got.Note, got.Confirm, got.Reason, got.Warning, c.want.Note, c.want.Confirm, c.want.Reason)
		}
	}
}

func TestBuiltinSpecsAreWordedByTheProfile(t *testing.T) {
	l := bundled(t)
	specs := Specs(l)
	for id, want := range map[string]api.InputSpec{
		"guide": {Label: "Guidance", Placeholder: "what the next round should look at"},
		"mute":  {Label: "Mute for", Placeholder: "2d, 36h"},
	} {
		s, _ := specByID(specs, id)
		if s.Input == nil || s.Input.Label != want.Label || s.Input.Placeholder != want.Placeholder {
			t.Errorf("%s input %+v", id, s.Input)
		}
	}
	plan := PlanSpecs(l)
	if s, _ := specByID(plan, "guide"); s.Input == nil || s.Input.Placeholder != "what the round should look at" || s.Label != "Guide" {
		t.Errorf("plan guide %+v", s)
	}
}

// TestActionRefuseIsTheOldEnabled checks the bundled [[action.refuse]]
// cases disable each intent exactly where its enabled predicate did, with
// the same reason.
func TestActionRefuseIsTheOldEnabled(t *testing.T) {
	l := bundled(t)
	old := map[dintent.Name]func(f Facts) (bool, string){
		dintent.Act: func(f Facts) (bool, string) { return f.yours(), "Not yours: take it over first" },
		dintent.Address: func(f Facts) (bool, string) {
			return f.Mine || f.Override.Adopted || f.authorWorkspace(), "Not yours: take it over first"
		},
		dintent.Nudge:  func(f Facts) (bool, string) { return f.NudgeRefusal == "", f.NudgeRefusal },
		dintent.Review: func(Facts) (bool, string) { return true, "" },
	}
	rows := []func(f *Facts){
		func(*Facts) {},
		func(f *Facts) { f.Mine = true },
		func(f *Facts) { f.Override.Adopted = true },
		func(f *Facts) { f.Workspace.ID = "w" },
		func(f *Facts) { f.Workspace = api.Workspace{ID: "w", IsReview: true} },
		func(f *Facts) { f.NudgeRefusal = "it waits on nobody" },
		func(f *Facts) { f.Row.Unit = unit.Unit{Provider: "thread", Key: "1"} },
	}
	for i, set := range rows {
		f := Facts{Intents: l, PR: r7, Owner: "acme", Repo: "api", Row: api.Row{Unit: r7}, Open: true, Tracked: true, NudgeWho: []string{"@ana"}}
		set(&f)
		for _, st := range States(f) {
			was, ok := old[dintent.Name(st.ID)]
			if !ok {
				continue
			}
			enabled, why := was(f)
			if st.Enabled != enabled || !enabled && st.Reason != why {
				t.Errorf("row %d, %s: enabled %v reason %q, want %v %q", i, st.ID, st.Enabled, st.Reason, enabled, why)
			}
		}
	}
}

func TestWarningShowsOnlyOnAQuestion(t *testing.T) {
	l := bundled(t)
	const warning = "Starts a new round; the agent's open question is replaced"
	for _, question := range []bool{false, true} {
		f := Facts{Intents: l, PR: r7, Owner: "acme", Repo: "api", Row: api.Row{Unit: r7}, Open: true, Tracked: true, Question: question, NudgeWho: []string{}}
		st := stateOf(t, f, string(dintent.Review))
		if want := map[bool]string{true: warning}[question]; st.Warning != want {
			t.Errorf("question %v: review warning %q, want %q", question, st.Warning, want)
		}
	}
}

func TestARefusalNamesTheProfileTextItShows(t *testing.T) {
	l := bundled(t)
	f := Facts{Intents: l, PR: r7, Owner: "acme", Repo: "api", Row: api.Row{Unit: r7}, Open: true, Tracked: true, Mine: false, NudgeWho: []string{}}
	if st := stateOf(t, f, string(dintent.Address)); st.Enabled || st.Refusal != api.RefuseCase(0) {
		t.Errorf("address on another's pull request: %+v", st)
	}
	f.Mine = true
	if st := stateOf(t, f, "take_over"); st.Enabled || st.Refusal != api.BuiltinWhy("take_over") {
		t.Errorf("take_over of mine: %+v", st)
	}
	if st := stateOf(t, f, "take_over"); st.Refusal == "" || st.Reason == "" {
		t.Errorf("take_over of mine: %+v", st)
	}
}

func TestADisabledBuiltinKeepsItsRefusalCodeWithoutAWhy(t *testing.T) {
	if st := texted(nil, "take_over", api.ActionState{ID: "take_over"}, nil); st.Refusal != api.BuiltinWhy("take_over") {
		t.Errorf("take_over with no why: %+v", st)
	}
}
