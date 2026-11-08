package web

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// quietClose is a pending item of a workflow that waits for you to apply it on task t-06bec1c03a26fadf, derived
// from a thread, resolving its two open items as moot and posting nothing.
func quietClose() api.PlanItem {
	return api.PlanItem{ID: "fake-close/task/t-06bec1c03a26fadf/ab", Workflow: "fake-close", Summary: "Close a quiet task; posts nothing",
		Decision: "pending", Default: "wait", Reason: "waits for you", Unit: unit.Unit{Provider: "task", Key: "t-06bec1c03a26fadf"},
		Label: "task:t-06bec1c03a26fadf", Steps: []string{"items.resolve"},
		Does: []api.PlanStep{{Use: "items.resolve", Reach: "local", Args: map[string]string{"resolution": "moot"},
			Items: []string{"Ana asks for the retry numbers", "Bea asks when it ships"}}},
		Target: &api.PlanTarget{Task: "t-06bec1c03a26fadf", Title: "Answer Ana about the retries",
			Units: []api.PlanUnit{{Label: "#general", Link: "https://chat.example/c1"}, {Label: "#ops"}}},
		Why:     api.Why{Trigger: "sweep", Matched: []api.Fact{{Name: "task.items_open", Value: "2"}}},
		Actions: []api.ActionState{}}
}

type planShape struct {
	Text    string
	Links   [][2]string
	Does    []string
	Items   []string
	Posts   string
	Why     []string
	Folded  bool
	Reasons []string
}

// planShapeJS reads a plan entry n: its whole text, its links, what applying does, its items, whether it posts,
// and its why.
const planShapeJS = `
const all = (n, pred, out = []) => {
  if (n && Array.isArray(n.children)) { if (pred(n)) out.push(n); n.children.forEach((c) => all(c, pred, out)); }
  return out;
};
const byClass = (n, c) => all(n, (x) => String(x.attrs.class || "").split(" ").includes(c));
const text = (n) => (n ? n.textContent : "");
const shape = (n) => {
  const why = byClass(n, "plan-why")[0];
  return {
    Text: text(n),
    Links: all(n, (x) => x.tag === "a").map((a) => [a.attrs.href, text(a)]),
    Does: byClass(n, "plan-do").map((x) => text(byClass(x, "plan-do-what")[0])),
    Items: all(byClass(n, "plan-does")[0] || { children: [] }, (x) => x.tag === "li" && x.attrs.class === "plan-do-item").map(text),
    Posts: byClass(n, "plan-posts").map(text).join(""),
    Why: why ? all(why, (x) => x.tag === "li").map(text) : [],
    Folded: !!why && why.tag === "details",
    Reasons: byClass(n, "plan-reason").map(text),
  };
};
`

func runPlanPage(t *testing.T, locale string, main string, in any, out any) {
	t.Helper()
	strs := "../../profiles/default/strings.toml"
	if locale != "en" {
		strs = "../../profiles/default/locales/" + locale + "/strings.toml"
	}
	nodeRun(t, cockpitScript(t)+stringsPrelude(t, locale, strs)+planShapeJS, nil, main, in, out)
}

func wantPlanShape(t *testing.T, where string, got planShape, does, posts string, reasonGone string) {
	t.Helper()
	if !slices.ContainsFunc(got.Links, func(l [2]string) bool {
		return strings.Contains(l[0], "task=t-06bec1c03a26fadf") && l[1] == "Answer Ana about the retries"
	}) {
		t.Errorf("%s: links %q, want the task's title linked to its detail", where, got.Links)
	}
	if !slices.Contains(got.Links, [2]string{"https://chat.example/c1", "#general"}) || !strings.Contains(got.Text, "#ops") {
		t.Errorf("%s: links %q, want its conversations, linked when they have a link", where, got.Links)
	}
	if strings.Contains(got.Text, "task:t-06bec1c03a26fadf") {
		t.Errorf("%s: %q shows the raw unit", where, got.Text)
	}
	if !slices.Equal(got.Does, []string{does}) || !slices.Equal(got.Items, []string{"Ana asks for the retry numbers", "Bea asks when it ships"}) {
		t.Errorf("%s: does %q on %q, want %q on the two open items", where, got.Does, got.Items, does)
	}
	if got.Posts != posts {
		t.Errorf("%s: posts %q, want %q", where, got.Posts, posts)
	}
	if !got.Folded || len(got.Why) != 1 || !strings.Contains(got.Why[0], "task.items_open") {
		t.Errorf("%s: why %q folded %v", where, got.Why, got.Folded)
	}
	if reasonGone != "" && strings.Contains(got.Text, reasonGone) {
		t.Errorf("%s: %q reads the engine's English %q", where, got.Text, reasonGone)
	}
}

func TestThePlanSaysWhatApplyingDoesToWhichTaskAndWhy(t *testing.T) {
	for _, c := range []struct{ locale, does, posts, gone string }{
		{"en", "Resolve its open items as no longer relevant", "Posts nothing", ""},
		{"pt-BR", "Resolver os itens abertos como sem efeito", "Não publica nada", "waits for you"},
	} {
		var got planShape
		runPlanPage(t, c.locale, `process.stdout.write(JSON.stringify(shape(planItem(input))));`, quietClose(), &got)
		wantPlanShape(t, c.locale, got, c.does, c.posts, c.gone)
	}
}

func TestAStepThatPostsSaysSoAndAnUnknownStepReadsAsItIs(t *testing.T) {
	it := quietClose()
	it.Does = []api.PlanStep{{Use: "draft.propose", Reach: "local"}, {Use: "fake.step", Reach: "visible"}}
	var got planShape
	runPlanPage(t, "en", `process.stdout.write(JSON.stringify(shape(planItem(input))));`, it, &got)
	if !slices.Equal(got.Does, []string{"Draft a message you approve before it posts", "fake.step"}) || got.Posts != "" {
		t.Fatalf("%+v: a draft is worded, an unknown step is named, and a visible step leaves out posts nothing", got)
	}
}

func TestTheQueuesPlanGateShowsTheSameTargetAndItems(t *testing.T) {
	p := quietClose()
	g := planGate(&p)
	var got planShape
	runPlanPage(t, "pt-BR", `process.stdout.write(JSON.stringify(shape(gateCard({ id: "t-06bec1c03a26fadf" }, input))));`, g, &got)
	wantPlanShape(t, "gate", got, "Resolver os itens abertos como sem efeito", "Não publica nada", "")
}

func TestAPlanReasonReadsInTheCatalogsWords(t *testing.T) {
	var got []string
	items := []api.PlanItem{
		{Reason: "waits for you", ReasonKey: "waits for you"},
		{Reason: "waits for you while task.items_open 2: the agent is busy", ReasonKey: "waits for you while {facts}: {refusal}",
			ReasonParams: map[string]string{"facts": "task.items_open 2", "refusal": "the agent is busy"},
			ReasonFacts:  []api.Fact{{Name: "task.items_open", Value: "2"}}},
		{Reason: "failed 3 times: exit 1", ReasonKey: "failed {n} times: {error}", ReasonParams: map[string]string{"n": "3", "error": "exit 1"}},
		{Reason: "Open cockpit"},
		{Reason: "a refusal no catalog words"},
	}
	runPlanPage(t, "pt-BR", `process.stdout.write(JSON.stringify(input.map((it) => planReason(it))));`, items, &got)
	want := []string{"espera você", "espera você enquanto task.items_open = 2: the agent is busy", "falhou 3 vezes: exit 1", "Abrir o cockpit", "a refusal no catalog words"}
	if !slices.Equal(got, want) {
		t.Errorf("reasons %q, want %q", got, want)
	}
}

func TestThePlanGateCardSaysWhyItsItemWaits(t *testing.T) {
	p := quietClose()
	p.Reason, p.ReasonKey = "observe mode", "observe mode"
	var got planShape
	runPlanPage(t, "pt-BR", `process.stdout.write(JSON.stringify(shape(gateCard({ id: "t-06bec1c03a26fadf" }, input))));`, planGate(&p), &got)
	if !slices.Equal(got.Reasons, []string{"o workflow só observa: nada roda até você aplicar"}) {
		t.Errorf("reasons %q, want the item's reason in pt-BR", got.Reasons)
	}
}
