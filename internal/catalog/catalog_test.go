package catalog

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/turn"
)

var r7 = pr.Ref{Owner: "acme", Repo: "api", Number: 7}

func bundled(t *testing.T) *intentload.Loaded {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestKeysAreUniqueAndAvoidNavigation(t *testing.T) {
	specs := Specs(bundled(t))
	byKey := map[string]api.ActionSpec{}
	for _, s := range specs {
		if s.Key == "" {
			continue
		}
		if keys.Reserved(keys.TUI)[s.Key] {
			t.Errorf("%s takes the core key %q", s.ID, s.Key)
		}
		if other, ok := byKey[s.Key]; ok && other.Toggle != s.ID {
			t.Errorf("%s and %s share %q without toggling", other.ID, s.ID, s.Key)
		}
		byKey[s.Key] = s
	}
}

func TestBuiltinVerbs(t *testing.T) {
	want := map[string]string{"run_now": "approve", "skip": "dismiss", "dismiss": "dismiss", "answer": "answer",
		"draft.approve": "approve", "draft.edit": "edit", "draft.discard": "dismiss", "draft.retry": "retry"}
	for _, b := range builtinActions {
		if b.spec.Verb != want[b.spec.ID] {
			t.Errorf("%s: verb %q, want %q", b.spec.ID, b.spec.Verb, want[b.spec.ID])
		}
	}
	intents := map[string]string{"address": "delegate", "review": "delegate", "nudge": ""}
	for _, s := range Specs(bundled(t)) {
		if want, ok := intents[s.ID]; ok && s.Verb != want {
			t.Errorf("intent %s: verb %q, want %q", s.ID, s.Verb, want)
		}
	}
	plan := map[string]string{PlanApply: "approve", PlanDismiss: "dismiss"}
	for _, s := range PlanSpecs() {
		if s.Verb != plan[s.ID] {
			t.Errorf("plan %s: verb %q, want %q", s.ID, s.Verb, plan[s.ID])
		}
	}
}

func TestBundledIntentKeys(t *testing.T) {
	l := bundled(t)
	for name, want := range map[dintent.Name]string{"address": "F", "review": "V", "nudge": "N"} {
		if d, _ := l.Registry.Get(name); d.Key != want {
			t.Errorf("%s: %q, want %q", name, d.Key, want)
		}
	}
}

// TestEveryActionHasItsKey pins the key of every action, plan decision and
// intent the catalog serves.
func TestEveryActionHasItsKey(t *testing.T) {
	actions := map[string]string{
		"open": "O", "skip": "S", "run_now": "R", "guide": "G", "answer": "W", "continue": "C", "dismiss": "X",
		"pause": "H", "resume": "H", "mute": "M", "unmute": "M", "take_over": "A", "trust_run": "T", "unblock": "D",
	}
	plan := map[string]string{PlanApply: "Enter", PlanDismiss: "X", "guide": "G", "trust_run": "T", "unblock": "D"}
	intents := map[string]string{"address": "F", "review": "V", "nudge": "N"}
	served := map[string]string{}
	for _, s := range Specs(bundled(t)) {
		served[s.ID] = s.Key
	}
	planned := map[string]string{}
	for _, s := range PlanSpecs() {
		planned[s.ID] = s.Key
	}
	for _, c := range []struct {
		name      string
		want, got map[string]string
	}{{"action", actions, served}, {"plan", plan, planned}, {"intent", intents, served}} {
		for id, want := range c.want {
			if got := c.got[id]; got != want {
				t.Errorf("%s %s: key %q, want %q", c.name, id, got, want)
			}
		}
	}
	if !maps.Equal(builtin.ActionKeys, actions) {
		t.Errorf("builtin.ActionKeys %v", builtin.ActionKeys)
	}
}

func TestBuiltinActionKeysMatchTheCatalog(t *testing.T) {
	got := map[string]string{}
	for _, b := range builtinActions {
		if b.spec.Key != "" {
			got[b.spec.ID] = b.spec.Key
		}
	}
	if !maps.Equal(got, builtin.ActionKeys) {
		t.Fatalf("catalog %v, builtin.ActionKeys %v", got, builtin.ActionKeys)
	}
}

func TestActionFactsCoverTheActionSlot(t *testing.T) {
	sources := map[string]func(f *Facts){
		"pr.mine":                            func(f *Facts) { f.Mine = true },
		"pr.author":                          func(f *Facts) { f.Author = "ana" },
		"pr.state":                           func(f *Facts) { f.State = "OPEN" },
		"pr.slug":                            func(f *Facts) { f.Ref = r7 },
		"pr.owner":                           func(f *Facts) { f.Ref = r7 },
		"pr.repo":                            func(f *Facts) { f.Ref = r7 },
		"pr.base":                            func(f *Facts) { f.Base = "main" },
		"pr.cross_repository":                func(f *Facts) { f.CrossRepository = true },
		"pr.draft":                           func(f *Facts) { f.Draft = true },
		"pr.review_requested_me":             func(f *Facts) { f.ReviewRequestedMe = true },
		"row.kind":                           func(f *Facts) { f.Row.Kind = "review" },
		"row.open":                           func(f *Facts) { f.Open = true },
		"row.tracked":                        func(f *Facts) { f.Tracked = true },
		"row.adopted":                        func(f *Facts) { f.Override.Adopted = true },
		"row.held":                           func(f *Facts) { f.Override.Hold = true },
		"row.muted":                          func(f *Facts) { f.Muted = true },
		"row.question":                       func(f *Facts) { f.Question = true },
		"row.pending":                        func(f *Facts) { f.Row.Pending = &api.PendingRound{} },
		"row.intake":                         func(f *Facts) { f.Intakes = []Intake{{Intake: api.Intake{Seq: 1}}} },
		"row.author_workspace":               func(f *Facts) { f.Workspace.ID = "w" },
		"row.yours":                          func(f *Facts) { f.Mine = true },
		"row.review_workspace":               func(f *Facts) { f.Workspace = api.Workspace{ID: "w", IsReview: true} },
		"row.nudge.who":                      func(f *Facts) { f.NudgeWho = []string{"@ana"} },
		"row.nudge.why":                      func(f *Facts) { f.NudgeWhy = "waits on a bot" },
		"row.nudge.refusal":                  func(f *Facts) { f.NudgeRefusal = "no" },
		"config.address_reviews":             func(f *Facts) { f.Config.AddressReviews = true },
		"config.address_checks":              func(f *Facts) { f.Config.AddressChecks = true },
		"config.followups_without_workspace": func(f *Facts) { f.Config.FollowupsWithoutWorkspace = true },
		"config.address_without_workspace":   func(f *Facts) { f.Config.AddressWithoutWorkspace = true },
	}
	zero := Facts{NudgeWho: []string{}}
	for name := range builtin.Env(builtin.ActionSlot, nil) {
		set, ok := sources[name]
		if !ok {
			t.Errorf("%s has no source in this table", name)
			continue
		}
		f := zero
		set(&f)
		if before, after := zero.Get(name), f.Get(name); before == nil || reflect.DeepEqual(before, after) {
			t.Errorf("%s reads %v before and %v after its source changed", name, before, after)
		}
	}
}

func TestAnActionTextThatFailsToRenderSaysSo(t *testing.T) {
	tmpl := template.Must(template.New("reason_off").Option("missingkey=error").Parse(`{{index .row.nudge.who 3}}`))
	p := intentload.Programs{ActionPrograms: intentload.ActionPrograms{ActionTexts: map[string]*template.Template{"reason_off": tmpl}}}
	got := actionText("ask-author", p, "reason_off", intentload.ActionFactMap(Facts{}.Get))
	if !strings.HasPrefix(got, "the profile has errors: ask-author: action.reason_off: ") {
		t.Fatalf("%q", got)
	}
}

func TestResolveRunRefusesTwoCandidates(t *testing.T) {
	defs := bundled(t).Registry.All()
	defs = append(defs, dintent.Def{Name: "ask-reviewer", Workspace: RoleReview, DrivesRow: true})
	ambiguous := dintent.NewRegistry(defs)
	if d, ok := ResolveRun(ambiguous, RoleReview); ok {
		t.Errorf("two intents drive review workspaces, run.resolve picked %s", d.Name)
	}
	if got, err := RunNowIntent(ambiguous, api.Workspace{ID: "w", IsReview: true}, api.Override{}, false); err == nil || err.Error() != "no single intent drives a row from a review workspace" {
		t.Errorf("ambiguous role: %q %v", got, err)
	}
}

func TestCatalogOffersIntentsOfTheRowProvider(t *testing.T) {
	l := bundled(t)
	ids := func(row api.Row) []string {
		var out []string
		for _, st := range States(Facts{Intents: l, Open: true, Tracked: true, Row: row, NudgeWho: []string{}}) {
			if d, ok := l.Registry.Get(dintent.Name(st.ID)); ok && d.Action != nil {
				out = append(out, st.ID)
			}
		}
		return out
	}
	if got := ids(api.Row{Unit: r7.Unit()}); len(got) == 0 {
		t.Fatal("a github row offers no intent")
	}
	if got := ids(api.Row{}); len(got) == 0 {
		t.Fatal("a row without a unit offers no intent")
	}
	if got := ids(api.Row{Unit: unit.Unit{Provider: "thread", Key: "1"}}); !slices.Equal(got, []string{string(dintent.Act)}) {
		t.Errorf("a thread row offers %v, want only the any-provider act", got)
	}
}

func TestCatalogOffersAnAnyIntentOnEveryRow(t *testing.T) {
	act := dintent.Def{Name: "act", Label: "Act", Workspace: RoleReview, UserFacing: true, Provider: dintent.AnyProvider, Action: &dintent.Action{Group: "turn"}}
	l := &intentload.Loaded{Registry: dintent.NewRegistry([]dintent.Def{act}), Programs: map[dintent.Name]intentload.Programs{}}
	for _, u := range []unit.Unit{r7.Unit(), {Provider: "thread", Key: "1"}} {
		if !slices.ContainsFunc(States(Facts{Intents: l, Open: true, Row: api.Row{Unit: u}, NudgeWho: []string{}}), func(s api.ActionState) bool { return s.ID == "act" }) {
			t.Errorf("act is not offered on %v", u)
		}
	}
}

func TestOriginOfASpec(t *testing.T) {
	for src, want := range map[string]int{SourceBuiltin: OriginBuiltin, "intents/review/intent.toml": OriginBundled,
		"/home/u/.config/x/intents/check/intent.toml": OriginUser} {
		if got := Origin(api.ActionSpec{Source: src}); got != want {
			t.Errorf("%s: %d, want %d", src, got, want)
		}
	}
}
