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
	d := Defaults().Proposals
	if time.Duration(d.RenudgeAfter) != 24*time.Hour || d.Approval("closure") != (draft.Approval{Mode: draft.Always}) {
		t.Fatalf("defaults %+v", d)
	}
	c := loadJSON(t, `{"proposals": {"closure": {"approval": {"mode": "window", "window": "1h"}}, "github-nudge": {"approval": {"mode": "observe"}}}}`)
	p := c.Proposals
	if time.Duration(p.RenudgeAfter) != 24*time.Hour {
		t.Fatalf("renudge_after left out lost its default: %v", time.Duration(p.RenudgeAfter))
	}
	if got := p.Approval("closure"); got != (draft.Approval{Mode: draft.Window, Window: time.Hour}) {
		t.Fatalf("closure %+v", got)
	}
	if got := p.Approval("github-nudge"); got != (draft.Approval{Mode: draft.Observe}) {
		t.Fatalf("github-nudge %+v", got)
	}
	if got := p.Approval("ask-owner"); got != (draft.Approval{Mode: draft.Always}) {
		t.Fatalf("an unset rule %+v", got)
	}
	c = loadJSON(t, `{"proposals": {"renudge_after": "2d", "stalled-nudge": {}}}`)
	if time.Duration(c.Proposals.RenudgeAfter) != 48*time.Hour || c.Proposals.Approval("stalled-nudge").Mode != draft.Always {
		t.Fatalf("%+v", c.Proposals)
	}
}

func TestProposalsRoundTrip(t *testing.T) {
	c := loadJSON(t, `{"proposals": {"renudge_after": "36h", "closure": {"approval": {"mode": "window", "window": "10m"}}}}`)
	raw, err := json.Marshal(c.Proposals)
	if err != nil {
		t.Fatal(err)
	}
	var back Proposals
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.RenudgeAfter != c.Proposals.RenudgeAfter || back.Approval("closure") != c.Proposals.Approval("closure") {
		t.Fatalf("%s -> %+v", raw, back)
	}
}

func TestProposalsRefuseABadApproval(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown mode":        `{"proposals": {"closure": {"approval": {"mode": "sometimes"}}}}`,
		"window without time": `{"proposals": {"closure": {"approval": {"mode": "window"}}}}`,
		"time without window": `{"proposals": {"closure": {"approval": {"mode": "always", "window": "1h"}}}}`,
		"no renudge":          `{"proposals": {"renudge_after": "0s"}}`,
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
