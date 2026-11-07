package builtin

import (
	"slices"
	"testing"
)

func TestLocalizedMatchesConcretePaths(t *testing.T) {
	for _, c := range []struct {
		s    Surface
		path []string
		want bool
	}{
		{SurfaceActions, []string{"take_over", "label"}, true},
		{SurfaceActions, []string{"mute", "input", "help"}, true},
		{SurfaceActions, []string{"take_over", "when"}, false},
		{SurfaceUI, []string{"views", "explore", "search", "presets", "[1]", "label"}, true},
		{SurfaceUI, []string{"views", "explore", "search", "presets", "[1]", "query"}, false},
		{SurfaceUI, []string{"views", "mine", "key"}, false},
		{SurfaceUI, []string{"lens", "age_now"}, true},
		{SurfaceViews, []string{"views", "mine", "key"}, false},
		{SurfaceViews, []string{"views", "mine", "label"}, true},
		{SurfaceStrings, []string{"strings", "Open cockpit"}, true},
		{SurfaceStrings, []string{"strings", "{n} drafts", "one"}, true},
		{SurfaceStrings, []string{"strings", "{n} drafts", "onee"}, false},
		{SurfaceStrings, []string{"formats", "time"}, true},
		{SurfaceNotices, []string{"answered"}, false},
		{SurfaceNotices, []string{NoticeDigestTitle}, true},
		{SurfaceIntent, []string{"help", "summary"}, false},
		{SurfaceIntent, []string{"label"}, true},
		{SurfaceIntent, []string{"action", "refuse", "[0]", "why"}, true},
		{SurfaceWorkflow, []string{"ask"}, true},
		{SurfaceWorkflow, []string{"trigger", "on"}, false},
		{SurfaceKeys, []string{"help", "list", "capture"}, true},
		{SurfaceActions, []string{"[0]"}, false},
		{SurfaceActions, nil, false},
	} {
		if got := Localized(c.s, c.path); got != c.want {
			t.Errorf("Localized(%s, %q) = %v, want %v", c.s, c.path, got, c.want)
		}
	}
}

func TestEveryLocalizedRowIsTextOrTemplate(t *testing.T) {
	seen := 0
	for _, k := range Grammar {
		if !k.Localized {
			continue
		}
		seen++
		plural := k.Kind == KindAny && slices.ContainsFunc(Grammar, func(f Key) bool {
			return f.In == k.In && f.Path == k.Path+".other" && f.Localized && f.Kind == KindText
		})
		if !slices.Contains([]Kind{KindText, KindTemplate}, k.Kind) && !plural {
			t.Errorf("%s %s is localized but %s", k.In, k.Path, kindName(k.Kind))
		}
	}
	if seen < 100 {
		t.Fatalf("only %d localized rows", seen)
	}
}

func TestEveryPersonNoticeIsANotice(t *testing.T) {
	for _, n := range PersonNotices {
		if _, ok := NoticeOf(n); !ok {
			t.Errorf("%s names no notice", n)
		}
	}
}

func TestSurfaceOf(t *testing.T) {
	for rel, want := range map[string]Surface{
		"actions.toml": SurfaceActions, "strings.toml": SurfaceStrings, "ui.toml": SurfaceUI,
		"intents/review/intent.toml": SurfaceIntent, "workflows/answer-unread/workflow.toml": SurfaceWorkflow,
	} {
		if got, ok := SurfaceOf(rel); !ok || got != want {
			t.Errorf("SurfaceOf(%q) = %q %v", rel, got, ok)
		}
	}
	for _, rel := range []string{"defaults.toml", "tools.toml", "intents/intent.toml", "x/intents/a/intent.toml", "workflows/a/text.md.tmpl", "locales/pt-BR/actions.toml"} {
		if s, ok := SurfaceOf(rel); ok {
			t.Errorf("SurfaceOf(%q) = %q", rel, s)
		}
	}
}
