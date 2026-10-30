package turn

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// pluginPayload is a payload as a source plugin's Payload answers it.
type pluginPayload struct {
	unit  unit.Unit
	facts map[string]any
}

func (p pluginPayload) Unit() unit.Unit { return p.unit }
func (p pluginPayload) Arg() string     { return "thread:" + p.unit.Key }
func (p pluginPayload) Wording() Wording {
	return Wording{Noun: "talk", This: "this talk", Ref: "talk " + p.unit.Key, Short: p.unit.Key}
}
func (p pluginPayload) Fact(name string) (any, bool) {
	v, ok := p.facts[name]
	return v, ok
}
func (p pluginPayload) Contents() string { return "Ana: can you look at the export?" }

// pluginReads is a source plugin's payload reader; it keeps each request.
type pluginReads struct{ got []Request }

func (r *pluginReads) Payload(_ context.Context, req Request) (Payload, error) {
	r.got = append(r.got, req)
	return pluginPayload{unit: req.Unit, facts: map[string]any{"thread.key": req.Unit.Key}}, nil
}

// TestTheTurnPayloadComesFromThePlugin builds a turn on a plugin's unit:
// the reader gets every request field the plugin's Payload carries, and the
// turn names its unit, words it and reads its facts and contents as the
// plugin answered, with no provider's own path.
func TestTheTurnPayloadComesFromThePlugin(t *testing.T) {
	reads := &pluginReads{}
	verdict := &RecordedVerdict{Head: "c3", Endorsed: "yes", At: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)}
	req := Request{Intent: "discuss", Unit: thread1, Planned: PlannedFollowUp, Agent: "claude", BriefMissing: "the brief gave up",
		LastVerdict: verdict, Items: []string{"i-1"}, Task: "t-0123456789abcdef", Guidance: "be brief", Inputs: map[string]any{}}
	c, err := Builder{Intents: threadIntents(t), Payloads: map[unit.ProviderID]PayloadReader{"thread": reads}, Guides: guides{}}.
		Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(reads.got) != 1 {
		t.Fatalf("read %d payloads, want 1", len(reads.got))
	}
	got := reads.got[0]
	if got.LastVerdict != verdict || got.Planned != PlannedFollowUp || got.Agent != "claude" || got.BriefMissing != "the brief gave up" ||
		!reflect.DeepEqual(got.Items, []string{"i-1"}) || got.Task != "t-0123456789abcdef" || got.Guidance != "be brief" {
		t.Fatalf("the plugin was asked %+v", got)
	}
	if c.Unit != thread1 || c.Wording.This != "this talk" || c.PR() != nil {
		t.Fatalf("unit %v wording %+v pr %v", c.Unit, c.Wording, c.PR())
	}
	if !strings.Contains(c.Headline, "talk 1700000000.1") || !strings.HasSuffix(Prompt(c, "7"), "thread:1700000000.1` and follow what it prints.") {
		t.Fatalf("headline %q prompt %q", c.Headline, Prompt(c, "7"))
	}
	if v := PayloadFacts(c.Payload).Get("thread.key"); v != thread1.Key {
		t.Errorf("thread.key reads %v", v)
	}
	if ct, ok := c.Payload.(Contents); !ok || ct.Contents() != "Ana: can you look at the export?" {
		t.Errorf("the contents did not come with the payload: %T", c.Payload)
	}
	if c.BriefMissing != "the brief gave up" {
		t.Errorf("brief missing %q", c.BriefMissing)
	}
}
