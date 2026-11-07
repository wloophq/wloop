package intent

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func TestTimelineLabelsLoadPerLocale(t *testing.T) {
	pt, problems := LoadIn("pt-BR", bundledOnly(), bundledDefault(t), testBase(), nil)
	for _, p := range problems {
		if strings.Contains(p.File, TimelineFile) {
			t.Errorf("%v", p)
		}
	}
	en, _ := LoadIn("en", bundledOnly(), bundledDefault(t), testBase(), nil)
	if pt.Timeline.Events["held"].Label != "pausado" || en.Timeline.Events["held"].Label != "paused" || en.Timeline.Events["turn_sent"].Label != "turn sent" {
		t.Fatalf("%v %v", pt.Timeline.Events["held"], en.Timeline.Events["held"])
	}
	for _, k := range builtin.EventKinds {
		if en.Timeline.Events[k.Name].Label == "" || pt.Timeline.Events[k.Name].Label == "" {
			t.Errorf("%s has no label", k.Name)
		}
	}
	ptRound, _ := factNamed(pt.Timeline, "round")
	enRound, _ := factNamed(en.Timeline, "round")
	if ptRound.Label != "Round do agente, {{.duration}}" || ptRound.Running != "Round do agente em andamento" || enRound.Label != "Agent round, {{.duration}}" || ptRound.Tone != "loop" {
		t.Errorf("the round fact per locale: %+v %+v", ptRound, enRound)
	}
}

func TestTimelineNamesOnlyKnownEventKinds(t *testing.T) {
	dir := userDir(t, map[string]string{TimelineFile: "[events.helt]\nlabel = \"x\"\n\n[events.held]\nlabel = \"on hold\"\n\n[events.muted]\nlabel = \"  \"\n"})
	l, problems := LoadIn("en", userLayer(dir), bundledDefault(t), testBase(), nil)
	var unknown, blank bool
	for _, p := range problems {
		unknown = unknown || p.Field == "events.helt" && strings.Contains(p.Msg, `did you mean "held"?`)
		blank = blank || p.Field == "events.muted.label" && strings.Contains(p.Msg, "is required")
	}
	if !unknown || !blank || l.Timeline.Events["held"].Label != "on hold" || l.Timeline.Events["muted"].Label != "muted" {
		t.Fatalf("%v %v", l.Timeline.Events, problems)
	}
}

func factNamed(d TimelineDef, name string) (TimelineFact, bool) {
	for _, f := range d.Facts {
		if f.Name == name {
			return f, true
		}
	}
	return TimelineFact{}, false
}

func TestTimelineRulesNameDeclaredKinds(t *testing.T) {
	dir := userDir(t, map[string]string{TimelineFile: "[facts.round]\nstart = [\"nope\"]\nlabel = \"Round\"\n\n[facts.empty]\nlabel = \"{{.who}}\"\n"})
	l, problems := LoadIn("en", userLayer(dir), bundledDefault(t), testBase(), nil)
	var named, missing, text bool
	for _, p := range problems {
		named = named || p.Field == "facts.round.start" && strings.Contains(p.Msg, `"nope"`)
		missing = missing || p.Field == "facts.empty.start" && strings.Contains(p.Msg, "is required")
		text = text || p.Field == "facts.empty.label" && strings.Contains(p.Msg, "who")
	}
	if !named || !missing || !text {
		t.Fatalf("%v", problems)
	}
	if f, ok := factNamed(l.Timeline, "round"); !ok || slices.Contains(f.Start, "nope") {
		t.Fatalf("a fact that fails keeps the lower layer's: %+v %v", f, ok)
	}
}

func TestAPluginLayerAddsRulesByName(t *testing.T) {
	plugin := userDir(t, map[string]string{TimelineFile: "[facts.pushed]\nstart = [\"push\"]\nabsorb = [\"push\", \"check\"]\nlabel = \"{{.actor}} pushed\"\norder = 5\n"})
	user := userDir(t, map[string]string{TimelineFile: "[facts.round]\nstart = [\"turn_started\"]\nend = [\"turn_ended\"]\nlabel = \"Turn, {{.duration}}\"\n\n[facts.done]\nstart = [\"verb_done\"]\n"})
	layers := []profile.Layer{bundledOnly()[0], {Name: profile.PluginLayer("acme"), Source: plugin, Dir: plugin}, {Name: "user", Source: user, Dir: user}}
	l, problems := LoadIn("en", layers, bundledDefault(t), testBase(), nil)
	for _, p := range problems {
		if strings.Contains(p.File, TimelineFile) {
			t.Errorf("%v", p)
		}
	}
	pushed, ok := factNamed(l.Timeline, "pushed")
	if !ok || pushed.Order != 5 || pushed.Label != "{{.actor}} pushed" {
		t.Fatalf("the plugin's fact: %+v", l.Timeline.Facts)
	}
	round, _ := factNamed(l.Timeline, "round")
	if round.Label != "Turn, {{.duration}}" || round.Running != "" || len(round.Absorb) != 0 {
		t.Fatalf("a higher layer replaces a fact whole: %+v", round)
	}
	if _, ok := factNamed(l.Timeline, "done"); !ok {
		t.Fatalf("a fact may name a task verb's event: %+v", l.Timeline.Facts)
	}
	for i := 1; i < len(l.Timeline.Facts); i++ {
		if a, b := l.Timeline.Facts[i-1], l.Timeline.Facts[i]; a.Order > b.Order {
			t.Fatalf("facts out of order: %+v", l.Timeline.Facts)
		}
	}
}

func TestATimelineToneIsOneOfTheTones(t *testing.T) {
	dir := userDir(t, map[string]string{TimelineFile: "[events.held]\nlabel = \"on hold\"\ntone = \"loud\"\n\n[facts.round]\nstart = [\"turn_started\"]\ntone = \"loud\"\n"})
	l, problems := LoadIn("en", userLayer(dir), bundledDefault(t), testBase(), nil)
	var event, fact bool
	for _, p := range problems {
		event = event || p.Field == "events.held.tone"
		fact = fact || p.Field == "facts.round.tone"
	}
	if !event || !fact || l.Timeline.Events["held"].Tone == "loud" {
		t.Fatalf("%v %+v", problems, l.Timeline.Events["held"])
	}
	if f, _ := factNamed(l.Timeline, "round"); f.Tone == "loud" {
		t.Fatalf("%+v", f)
	}
}

func TestEveryCoreKindConstantIsDeclared(t *testing.T) {
	kinds := []string{
		turns.KindTurnSent, turns.KindTurnDelivered, turns.KindTurnWithdrawn, turns.KindTurnStarted, turns.KindTurnEnded, turns.KindAnswered,
		turns.KindAnswerRouted, turns.KindQuestionDismissed, turns.KindTurnResumed, turns.KindSessionStarted, turns.KindSessionLaunched,
		turns.KindSessionAbandoned, turns.KindMCPSeen, turns.KindTurnStopped, turns.KindStopBlocked, turns.KindSessionIdle, turns.KindSessionActive,
		turns.KindSessionClosed, turns.KindSessionTyped, turns.KindSessionPrompted, turns.KindTurnDismissedByRule, turns.KindAnswerRefused,
		turns.KindAnswerRead, turns.KindLateRecord, turns.KindSessionUnit, turns.KindSessionAdopted, turns.KindSessionDormant, turns.KindSessionWoken,
		turns.KindSessionMerged, views.KindViewApplied, timeline.KindRecord, gate.EventResolved, messages.KindChat,
	}
	declared := builtin.ValueNames(builtin.EventKinds)
	for _, k := range kinds {
		if !slices.Contains(declared, k) {
			t.Errorf("%s is no builtin.EventKinds", k)
		}
	}
}
