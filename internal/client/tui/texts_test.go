package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/client/texts"
)

// ptBR is the bundled pt-BR client strings, as the daemon serves them.
func ptBR(t *testing.T) *texts.Catalog {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "profiles", "default", "locales", "pt-BR", "strings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Strings map[string]any }
	if err := toml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	s := api.Strings{Locale: "pt-BR", Strings: map[string]api.Text{}}
	for k, v := range doc.Strings {
		switch x := v.(type) {
		case string:
			s.Strings[k] = api.Text{"other": x}
		case map[string]any:
			forms := api.Text{}
			for f, txt := range x {
				forms[f] = txt.(string)
			}
			s.Strings[k] = forms
		}
	}
	return texts.New(s)
}

func emptyPlanScreen(t *testing.T, words *texts.Catalog) string {
	m := newModel(t)
	m.words = words
	m.SetData(oneRow(), api.Health{})
	m.SetPlan(nil, nil)
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	return strings.Join(m.Render(100, 30, now), "\n")
}

func TestPlanEmptyStateInPtBR(t *testing.T) {
	screen := emptyPlanScreen(t, ptBR(t))
	for _, want := range []string{"Nada pendente ou bloqueado no plano.", "[Plano 0]"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q in:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "Nothing pending") {
		t.Errorf("English in the pt-BR plan:\n%s", screen)
	}
}

func TestTerminalIsEnglishWithoutStrings(t *testing.T) {
	if screen := emptyPlanScreen(t, nil); !strings.Contains(screen, "Nothing pending or blocked in the plan.") {
		t.Errorf("%s", screen)
	}
}

func TestRunReadsTheServedStrings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow(), strings: &api.Strings{Locale: "pt-BR", Strings: map[string]api.Text{"Plan {n}": {"other": "Plano {n}"}}}}
		term, done := start(t, d)
		eventually(t, "the pt-BR plan tab", func() bool { return strings.Contains(term.screen(), "Plano 0") })
		close(term.keys)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestTheTerminalWordsItsOwnErrors(t *testing.T) {
	m := newModel(t)
	m.words = ptBR(t)
	_, _, err := openLink(context.Background(), Config{}, Command{})
	m.Done(Command{Action: "Abrir", Label: "t-1"}, nil, err)
	if want := "Abrir t-1 falhou: abrir links não é suportado aqui"; m.status != want {
		t.Fatalf("%q, want %q", m.status, want)
	}
	if err.Error() != "opening links is not supported here" {
		t.Fatalf("%q", err)
	}
}
