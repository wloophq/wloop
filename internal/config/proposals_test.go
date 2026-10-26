package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func TestProposalsDefaultsAndParsing(t *testing.T) {
	d := Defaults()
	if time.Duration(d.Drafts.RepeatAfter) != 24*time.Hour || d.Proposals.Approval("closure") != (draft.Approval{Mode: draft.Wait}) {
		t.Fatalf("defaults %+v", d.Proposals)
	}
	c := loadJSON(t, `{"proposals": {"closure": {"approval": {"mode": "delay", "window": "1h"}}, "github-nudge": {"approval": {"mode": "record"}}}}`)
	p := c.Proposals
	if time.Duration(c.Drafts.RepeatAfter) != 24*time.Hour {
		t.Fatalf("drafts.repeat_after left out lost its default: %v", time.Duration(c.Drafts.RepeatAfter))
	}
	if got := p.Approval("closure"); got != (draft.Approval{Mode: draft.Delay, Window: time.Hour}) {
		t.Fatalf("closure %+v", got)
	}
	if got := p.Approval("github-nudge"); got != (draft.Approval{Mode: draft.DryRun}) {
		t.Fatalf("github-nudge %+v", got)
	}
	if got := p.Approval("ask-owner"); got != (draft.Approval{Mode: draft.Wait}) {
		t.Fatalf("an unset rule %+v", got)
	}
	c = loadJSON(t, `{"drafts": {"repeat_after": "2d"}, "proposals": {"stalled-nudge": {}}}`)
	if time.Duration(c.Drafts.RepeatAfter) != 48*time.Hour || c.Proposals.Approval("stalled-nudge").Mode != draft.Wait {
		t.Fatalf("%+v", c.Proposals)
	}
}

func TestProposalsRoundTrip(t *testing.T) {
	c := loadJSON(t, `{"proposals": {"closure": {"approval": {"mode": "delay", "window": "10m"}}}}`)
	raw, err := json.Marshal(c.Proposals)
	if err != nil {
		t.Fatal(err)
	}
	var back Proposals
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Approval("closure") != c.Proposals.Approval("closure") {
		t.Fatalf("%s -> %+v", raw, back)
	}
}

func TestDraftsRepeatAfterMustBePositive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"drafts": {"repeat_after": "0s"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, tools()); err == nil || !strings.Contains(err.Error(), "drafts.repeat_after") {
		t.Fatalf("err = %v", err)
	}
}

func TestProposalsRefuseABadApproval(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown mode":        `{"proposals": {"closure": {"approval": {"mode": "sometimes"}}}}`,
		"window without time": `{"proposals": {"closure": {"approval": {"mode": "delay"}}}}`,
		"time without window": `{"proposals": {"closure": {"approval": {"mode": "wait", "window": "1h"}}}}`,
		"not an object":       `{"proposals": {"closure": 3}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(path, tools()); err == nil || !strings.Contains(err.Error(), "proposals") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
