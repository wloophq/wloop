package tui

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestThePlanViewWordsStepsAndReasonsFromTheCatalog(t *testing.T) {
	m := newModel(t)
	m.words = ptBR(t)
	m.SetData(oneRow(), api.Health{})
	it := api.PlanItem{ID: "fake-close/task/t-1/ab", Workflow: "fake-close", Summary: "Fechar uma tarefa quieta", Decision: "pending", Default: "wait",
		Unit: unit.Unit{Provider: "task", Key: "t-1"}, Label: "Answer Ana", Steps: []string{"items.resolve"},
		Does: []api.PlanStep{{Use: "items.resolve", Reach: "local", Says: "Resolve its open items as {resolution}",
			Args: map[string]string{"resolution": "moot", "resolution_says": "resolved as moot"}, Items: []string{"Ana asks for the numbers"}}},
		Reason: "waits for you while task.items_open 2", ReasonKey: "waits for you while {facts}",
		ReasonParams: map[string]string{"facts": "task.items_open 2"}, ReasonFacts: []api.Fact{{Name: "task.items_open", Value: "2"}}}
	m.SetPlan([]api.PlanItem{it}, nil)
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	screen := strings.Join(m.Render(140, 40, now), "\n")
	for _, want := range []string{"Fechar uma tarefa quieta", "Resolver os itens abertos como sem efeito", "Ana asks for the numbers", "espera você enquanto task.items_open = 2", "Não publica nada"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q in:\n%s", want, screen)
		}
	}
	for _, raw := range []string{"items.resolve", "waits for you"} {
		if strings.Contains(screen, raw) {
			t.Errorf("raw %q in:\n%s", raw, screen)
		}
	}
}

func TestAPlanReasonReadsInTheTerminalsCatalog(t *testing.T) {
	m := newModel(t)
	m.words = ptBR(t)
	m.SetViews(api.ViewsCatalog{Lens: testLensWords, Facets: []api.FacetSpec{{ID: "kind", Label: "Tipo", Fact: "task.prop.kind", Labels: map[string]string{"thread": "Conversa"}}}})
	held := api.PlanItem{Reason: "waits for you while task.prop.kind thread: the task holds no open item", ReasonKey: "waits for you while {facts}: {refusal}",
		ReasonParams: map[string]string{"facts": "task.prop.kind thread", "refusal": "the task holds no open item"},
		ReasonFacts:  []api.Fact{{Name: "task.prop.kind", Value: "thread"}}}
	for _, c := range []struct {
		it   api.PlanItem
		want string
	}{
		{api.PlanItem{Reason: "the task holds no open item"}, "a tarefa não tem item aberto"},
		{held, "espera você enquanto Tipo: Conversa: a tarefa não tem item aberto"},
	} {
		if got := m.planReason(c.it); got != c.want {
			t.Errorf("%q reads %q, want %q", c.it.Reason, got, c.want)
		}
	}
}

func TestThePlanTabStaysWithNoRowViews(t *testing.T) {
	m := NewModel()
	m.SetCatalog(api.CatalogResponse{})
	m.SetViews(api.ViewsCatalog{Views: []api.TaskViewSpec{{ID: "focus", Label: "Focus", Rail: true}}, Lens: testLensWords})
	m.setView(m.planView())
	m.SetPlugins([]api.Plugin{{ID: "chat", Name: "Chat", Enabled: true}})
	if m.view != m.planView() {
		t.Errorf("with no row views the plugins moved the view to %d, off the plan %d", m.view, m.planView())
	}
}
