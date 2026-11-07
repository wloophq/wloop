package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// languageRig is a draft rig whose thread's provider tells its language through slack.language when tells,
// whose workflow wf has an English and a pt-BR text, and whose user reads userLocale.
func languageRig(t *testing.T, tells bool, threadLanguage, userLocale string) *draftRig {
	t.Helper()
	r := newDraftRig(t, draftWF("wf", "purpose", "nudge"), "Any news?")
	p := filepath.Join(r.cfg.Profile, "locales", "pt-BR", "workflows", "wf", defaultDraftText)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("Alguma novidade?"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.deps.Languages = map[unit.ProviderID]string{}
	if tells {
		r.deps.Languages[draftThread.Provider] = "slack.language"
	}
	r.units.Put(draftThread, workflow.Facts{"slack.language": threadLanguage})
	r.s.e.intentState().useLocale(func() string { return userLocale })
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-72*time.Hour)))
	return r
}

func (r *draftRig) proposedText() (text, language string) {
	r.t.Helper()
	r.run(draftThread)
	ds := r.drafts()
	if len(ds) != 1 {
		r.t.Fatalf("%d drafts, want one", len(ds))
	}
	return ds[0].Text, ds[0].Language
}

func TestADraftIsWrittenInTheUnitsLanguage(t *testing.T) {
	t.Parallel()
	r := languageRig(t, true, "pt", "en")
	if text, lang := r.proposedText(); text != "Alguma novidade?" || lang != "pt-BR" {
		t.Errorf("text %q language %q", text, lang)
	}
}

func TestADraftFallsBackToTheUsersLocale(t *testing.T) {
	t.Parallel()
	r := languageRig(t, true, "", "pt-BR")
	if text, lang := r.proposedText(); text != "Alguma novidade?" || lang != "pt-BR" {
		t.Errorf("text %q language %q", text, lang)
	}
}

func TestALanguageWithNoTemplateWritesTheDefault(t *testing.T) {
	t.Parallel()
	r := languageRig(t, true, "es", "pt-BR")
	if text, lang := r.proposedText(); text != "Any news?" || lang != "en" {
		t.Errorf("text %q language %q", text, lang)
	}
}

func TestAProviderWithNoLanguageFactUsesTheUsersLocale(t *testing.T) {
	t.Parallel()
	r := languageRig(t, false, "pt", "en")
	if text, lang := r.proposedText(); text != "Any news?" || lang != "en" {
		t.Errorf("text %q language %q", text, lang)
	}
}

func TestDraftLanguage(t *testing.T) {
	t.Parallel()
	available := []string{"en", "pt-BR"}
	languages := map[unit.ProviderID]string{"acme": "acme.language"}
	for _, c := range []struct {
		name     string
		provider unit.ProviderID
		fact     any
		want     string
	}{
		{"a language matches its regional locale", "acme", "pt", "pt-BR"},
		{"an exact tag", "acme", "pt-BR", "pt-BR"},
		{"a language with no locale writes the default", "acme", "es", "en"},
		{"no value is the user's", "acme", "", "pt-BR"},
		{"a value of another type is the user's", "acme", 3, "pt-BR"},
		{"a provider with no language fact is the user's", "other", "en", "pt-BR"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := draftLanguage(workflow.Facts{"acme.language": c.fact}, languages[c.provider], "pt-BR", available); got != c.want {
				t.Errorf("draftLanguage = %q, want %q", got, c.want)
			}
		})
	}
}
