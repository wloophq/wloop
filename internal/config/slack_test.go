package config

import (
	"testing"
	"time"
)

func TestSlackExtractionDefaults(t *testing.T) {
	e := loadRaw(t, `{}`).Slack.Extraction
	if !e.On() || e.TierName() != "fast" || e.LanguageName() != "English" || e.MaxCallUSD() != 0.10 || e.MaxDayUSD() != 3.0 ||
		e.MaxDayCalls() != 500 || e.QuietFor() != 90*time.Second || e.MaxWaitFor() != 10*time.Minute || e.Window() != 40000 || e.GiveUpAfter() != 3 {
		t.Fatalf("defaults %+v", e)
	}
	if w := loadRaw(t, `{}`).Items.Heat.WarmFor; w != 0 {
		t.Fatalf("warm_for default %v", w)
	}
}

func TestSlackExtractionParses(t *testing.T) {
	c := loadRaw(t, `{"slack": {"extraction": {"enabled": false, "tier": "balanced", "language": "Portuguese",
		"max_call_usd": 0.2, "max_day_usd": 1, "max_day_calls": 50, "quiet": "30s", "max_wait": "5m", "window_chars": 1000, "max_failures": 5}},
		"items": {"heat": {"warm_for": "2d"}}}`)
	e := c.Slack.Extraction
	if e.On() || e.TierName() != "balanced" || e.LanguageName() != "Portuguese" || e.MaxCallUSD() != 0.2 || e.MaxDayUSD() != 1 ||
		e.MaxDayCalls() != 50 || e.QuietFor() != 30*time.Second || e.MaxWaitFor() != 5*time.Minute || e.Window() != 1000 || e.GiveUpAfter() != 5 {
		t.Fatalf("parsed %+v", e)
	}
	if time.Duration(c.Items.Heat.WarmFor) != 48*time.Hour {
		t.Fatalf("warm_for %v", c.Items.Heat.WarmFor)
	}
}

func TestSlackTurnsMaxChars(t *testing.T) {
	if got := loadRaw(t, `{}`).Slack.Turns.Chars(); got != 30000 {
		t.Fatalf("default %d", got)
	}
	if got := loadRaw(t, `{"slack": {"turns": {"max_chars": 12000}}}`).Slack.Turns.Chars(); got != 12000 {
		t.Fatalf("set %d", got)
	}
	for _, small := range []int{1, 40, 1999} {
		if got := (SlackTurns{MaxChars: small}).Chars(); got != MinSlackTurnChars {
			t.Fatalf("%d reads as %d", small, got)
		}
	}
}

// slackPlacement places Slack units: a thread by its id, every thread of a
// channel by the channel's id, and a channel by its name, which only the
// Slack default reads.
const slackPlacement = `{"placement": [
	{"match": "slack:T0000000001/C0000000001/1700000000.000100", "repo": "acme/api", "checkout": "main", "role": "work", "shared": true},
	{"match": "slack:T0000000001/C0000000002/*", "repo": "acme/web", "role": "work"},
	{"match": "slack:#eng-*", "repo": "acme/api", "checkout": "main", "role": "work"}
]}`

func TestSlackPlacementExampleLoads(t *testing.T) {
	m := loadRaw(t, slackPlacement).PlacementMap()
	if len(m.Rules) != 3 || !m.Rules[0].Shared || m.Rules[2].Match != "slack:#eng-*" {
		t.Fatalf("%+v", m)
	}
}

func TestSlackExtractionContextAndEscalation(t *testing.T) {
	e := loadRaw(t, `{}`).Slack.Extraction
	if e.ContextN() != 10 || e.ContextBudget() != 4000 || e.EscalateOver() != 0 {
		t.Fatalf("defaults %d %d %d", e.ContextN(), e.ContextBudget(), e.EscalateOver())
	}
	e = loadRaw(t, `{"slack": {"extraction": {"context_messages": 3, "context_chars": 900, "escalate_over_chars": 6000}}}`).Slack.Extraction
	if e.ContextN() != 3 || e.ContextBudget() != 900 || e.EscalateOver() != 6000 {
		t.Fatalf("set %d %d %d", e.ContextN(), e.ContextBudget(), e.EscalateOver())
	}
	e = loadRaw(t, `{"slack": {"extraction": {"context_messages": -1, "context_chars": -1, "escalate_over_chars": -1}}}`).Slack.Extraction
	if e.ContextN() != 0 || e.ContextBudget() != 0 || e.EscalateOver() != 0 {
		t.Fatalf("off %d %d %d", e.ContextN(), e.ContextBudget(), e.EscalateOver())
	}
}
