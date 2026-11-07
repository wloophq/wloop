package web

import (
	"strings"
	"testing"
)

func TestEventLabelReadsTheCatalog(t *testing.T) {
	var got struct {
		Label, Unknown string
		Loop, Other    bool
	}
	nodeRun(t, jsConst(t, appJS(t), "catalog"), []string{"applyCatalog", "fieldOf", "eventLabel", "loopEvent"},
		`applyCatalog({ events: [{ provider: "", kind: "held", label: "pausado", loop: true }] });
process.stdout.write(JSON.stringify({ label: eventLabel("held"), loop: loopEvent("held"), unknown: eventLabel("some_kind"), other: loopEvent("some_kind") }));`, nil, &got)
	if got.Label != "pausado" || !got.Loop || got.Unknown != "some kind" || got.Other {
		t.Fatalf("%+v", got)
	}
	for _, gone := range []string{"EVENT_LABEL", "LOOP_EVENTS"} {
		if strings.Contains(appJS(t), gone) {
			t.Errorf("app.js still has %s; the core's event kinds are timeline.toml's", gone)
		}
	}
}

func TestFillWordPicksThePluralForm(t *testing.T) {
	var got []string
	nodeRun(t, staticJS(t, "i18n.js"), []string{"fillWord"}, `
const en = '{{.n}} open {{plural .n "one" "question" "other" "questions"}} in the brief';
const pt = '{{.n}} {{plural .n "one" "pergunta aberta" "other" "perguntas abertas"}}';
const out = [fillWord(en, {n: 1}), fillWord(en, {n: 2})];
I18N.locale = "pt-BR";
out.push(fillWord(pt, {n: 0}), fillWord(pt, {n: 3}), fillWord("{{.unit}}: {{.shown}}", {unit: "Item", shown: 2}));
process.stdout.write(JSON.stringify(out));`, nil, &got)
	want := []string{"1 open question in the brief", "2 open questions in the brief", "0 pergunta aberta", "3 perguntas abertas", "Item: 2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
}
