package intent_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// ptBundle is the bundled profile with a pt-BR file, which turns translation on.
func ptBundle(t *testing.T) fstest.MapFS {
	t.Helper()
	src := turn.BundledFS()
	out := fstest.MapFS{"locales/pt-BR/actions.toml": {Data: []byte("[pause]\nlabel = \"Pausar\"\n")}}
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(src, p)
		out[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func loadIn(t *testing.T, locale string, bundled fs.FS, user map[string]string) (*intentload.Loaded, intentload.Problems) {
	t.Helper()
	d := t.TempDir()
	for name, body := range user {
		f := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	layers := append(profile.BundledChain(), profile.Layer{Name: "user", Source: d, Dir: d})
	return intentload.LoadIn(locale, layers, bundled, base, turn.CheckTemplate)
}

// startWith is the bundled start intent's manifest with old replaced by new.
func startWith(t *testing.T, old, new string) string {
	t.Helper()
	b, err := fs.ReadFile(turn.BundledFS(), "intents/start/intent.toml")
	if err != nil || !strings.Contains(string(b), old) {
		t.Fatalf("%v: no %q", err, old)
	}
	return strings.Replace(string(b), old, new, 1)
}

const startNote = "Starts an agent session on the task's brief"

func TestARevertedOverlayKeepsTheBundledIntentInEveryLocale(t *testing.T) {
	bundled := ptBundle(t)
	for name, manifest := range map[string]string{
		"an unknown key":  "bogus = 1\n" + startWith(t, `label     = "Start"`, `label     = "Mine"`),
		"a broken action": startWith(t, `note = "`+startNote+`"`, `note = "Runs {{if}}"`),
	} {
		for _, locale := range []string{"en", "pt-BR"} {
			plain, _ := loadIn(t, locale, bundled, nil)
			want, _ := plain.Registry.Get("start")
			l, problems := loadIn(t, locale, bundled, map[string]string{"intents/start/intent.toml": manifest})
			d, ok := l.Registry.Get("start")
			if !ok || d.Label != want.Label || len(l.Reverted["start"]) == 0 {
				t.Errorf("%s, %s: start loaded %v, label %q, reverted %v: %v", name, locale, ok, d.Label, l.Reverted["start"], problems)
			}
		}
	}
}

// startNoteOf is the start intent's action note as l renders it.
func startNoteOf(t *testing.T, l *intentload.Loaded) string {
	t.Helper()
	var b strings.Builder
	tm := l.Programs["start"].Action.ActionTexts["note"]
	if tm == nil || tm.Execute(&b, intentload.SampleActionFacts()) != nil {
		return ""
	}
	return b.String()
}

func TestATranslationCallsNothingItsEnglishTextDoesNot(t *testing.T) {
	bundled := ptBundle(t)
	plain, _ := loadIn(t, "pt-BR", bundled, nil)
	bundledNote := startNoteOf(t, plain)
	for _, note := range []string{`Roda {{nofunc}}`, `Roda {{template \"nope\"}}`, `Roda {{$.x.y}}`} {
		l, problems := loadIn(t, "pt-BR", bundled, map[string]string{"locales/pt-BR/intents/start/intent.toml": "[action]\nnote = \"" + note + "\"\n"})
		if _, ok := l.Registry.Get("start"); !ok {
			t.Errorf("%s: start is gone: %v", note, problems)
			continue
		}
		if got := startNoteOf(t, l); got != bundledNote {
			t.Errorf("%s: note %q, want the bundled %q", note, got, bundledNote)
		}
		var hit bool
		for _, p := range problems {
			hit = hit || strings.HasSuffix(p.File, filepath.FromSlash("locales/pt-BR/intents/start/intent.toml")) && p.Field == "action.note"
		}
		if !hit {
			t.Errorf("%s: no problem names the translation: %v", note, problems)
		}
	}
}
