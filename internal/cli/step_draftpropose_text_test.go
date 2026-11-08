package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// templateLayers are the bundled layer, read from a map holding wf's text in English and pt-BR, and a user
// layer holding user's files, by path under its directory.
func templateLayers(t *testing.T, user map[string]string) ([]profile.Layer, fs.FS) {
	t.Helper()
	bundled := fstest.MapFS{
		"workflows/wf/text.md.tmpl":               {Data: []byte("Any news?")},
		"locales/pt-BR/workflows/wf/text.md.tmpl": {Data: []byte("Alguma novidade?")},
	}
	dir := t.TempDir()
	for rel, body := range user {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return profile.Chain(config.Config{Profile: dir}), bundled
}

func renderTemplate(t *testing.T, layers []profile.Layer, bundled fs.FS, locale string, data any) (string, string) {
	t.Helper()
	tmpl, written, err := draftTemplate(layers, bundled, "wf", defaultDraftText, draft.Message, locale)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	return b.String(), written
}

func TestADraftTemplateResolvesInTheGivenLocale(t *testing.T) {
	t.Parallel()
	layers, bundled := templateLayers(t, nil)
	if body, written := renderTemplate(t, layers, bundled, "pt-BR", nil); body != "Alguma novidade?" || written != "pt-BR" {
		t.Errorf("pt-BR: %q written in %q", body, written)
	}
}

func TestADraftTemplateWithNoTranslationIsTheDefault(t *testing.T) {
	t.Parallel()
	layers, bundled := templateLayers(t, nil)
	if body, written := renderTemplate(t, layers, bundled, "es", nil); body != "Any news?" || written != "en" {
		t.Errorf("es: %q written in %q", body, written)
	}
}

func TestAnUnchangedUserCopyKeepsTheTranslation(t *testing.T) {
	t.Parallel()
	layers, bundled := templateLayers(t, map[string]string{"workflows/wf/text.md.tmpl": "Any news?"})
	if body, written := renderTemplate(t, layers, bundled, "pt-BR", nil); body != "Alguma novidade?" || written != "pt-BR" {
		t.Errorf("unchanged copy: %q written in %q", body, written)
	}
}

func TestAnEditedUserCopyWinsInEveryLanguage(t *testing.T) {
	t.Parallel()
	layers, bundled := templateLayers(t, map[string]string{"workflows/wf/text.md.tmpl": "Any update?"})
	for _, locale := range []string{"pt-BR", "en"} {
		if body, written := renderTemplate(t, layers, bundled, locale, nil); body != "Any update?" || written != "en" {
			t.Errorf("%s: %q written in %q", locale, body, written)
		}
	}
}

func TestAUserTranslationIsWrittenInItsLocale(t *testing.T) {
	t.Parallel()
	layers, bundled := templateLayers(t, map[string]string{"locales/pt-BR/workflows/wf/text.md.tmpl": "E aí?"})
	if body, written := renderTemplate(t, layers, bundled, "pt-BR", nil); body != "E aí?" || written != "pt-BR" {
		t.Errorf("user translation: %q written in %q", body, written)
	}
}

func TestADraftTemplateCanUsePlural(t *testing.T) {
	t.Parallel()
	layers, bundled := templateLayers(t, map[string]string{
		"workflows/wf/text.md.tmpl":               `{{plural (len .) "one" "this" "other" "these"}}`,
		"locales/pt-BR/workflows/wf/text.md.tmpl": `{{plural (len .) "one" "isto" "other" "estes"}}`,
	})
	if body, _ := renderTemplate(t, layers, bundled, "pt-BR", []int{1, 2}); body != "estes" {
		t.Errorf("plural: %q", body)
	}
}

func BenchmarkADraftsLanguageAndTemplate(b *testing.B) {
	layers := profile.Chain(config.Config{})
	for b.Loop() {
		lang := draftLanguage(workflow.Facts{"slack.language": "pt"}, "slack.language", "en", intentload.Locales(layers, turn.BundledFS()))
		if _, _, err := draftTemplate(layers, turn.BundledFS(), "task-not-mine", defaultDraftText, draft.Message, lang); err != nil {
			b.Fatal(err)
		}
	}
}

func TestASlackDraftTemplateIsFoundUnderAUserGithubDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "plugins", "github"), 0o755); err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(dir, "plugins", "github")
	layers := append(profile.BundledChain(), profile.Layer{Name: "user", Source: dir, Dir: dir}, profile.Layer{Name: "user:github", Source: gh, Dir: gh})
	for _, file := range []string{defaultDraftText, defaultDraftWhy} {
		for _, locale := range []string{"en", "pt-BR"} {
			tmpl, written, err := draftTemplate(layers, turn.BundledFS(), "slack-stalled-nudge", file, draft.Message, locale)
			if err != nil || tmpl == nil || written != locale {
				t.Errorf("%s in %s: %v %v %q", file, locale, tmpl, err, written)
			}
		}
	}
}

func TestADraftTemplateNoLayerHoldsIsNotFound(t *testing.T) {
	t.Parallel()
	layers, bundled := templateLayers(t, nil)
	if _, _, err := draftTemplate(layers, bundled, "nope", defaultDraftText, draft.Message, "en"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err %v, want not exist", err)
	}
}
