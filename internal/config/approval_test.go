package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDraftsRepeatAfterDefaultsAndParsing(t *testing.T) {
	if d := Defaults(); time.Duration(d.Drafts.RepeatAfter) != 24*time.Hour {
		t.Fatalf("default %v", time.Duration(d.Drafts.RepeatAfter))
	}
	if c := loadJSON(t, `{"turns": {"per_day": 3}}`); time.Duration(c.Drafts.RepeatAfter) != 24*time.Hour {
		t.Fatalf("drafts.repeat_after left out lost its default: %v", time.Duration(c.Drafts.RepeatAfter))
	}
	if c := loadJSON(t, `{"drafts": {"repeat_after": "2d"}}`); time.Duration(c.Drafts.RepeatAfter) != 48*time.Hour {
		t.Fatalf("%v", time.Duration(c.Drafts.RepeatAfter))
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

func TestPluginDraftApprovalRefusesABadApproval(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown mode":        `{"github": {"drafts": {"approval": {"mode": "sometimes"}}}}`,
		"window without time": `{"github": {"drafts": {"approval": {"mode": "delay"}}}}`,
		"time without window": `{"github": {"drafts": {"approval": {"mode": "wait", "window": "1h"}}}}`,
		"not an object":       `{"github": {"drafts": {"approval": 3}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), "drafts.approval") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestAWorkflowsApprovalLoadsAsAnOverlay(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"workflows": {"w": {"drafts": {"approval": {"rule": [{"when": "slack.conversation == \"dm\"", "mode": "send"}]}}}}}`,
		`{"workflows": {"w": {"drafts": {"approval": {"rule": []}}}}}`,
		`{"workflows": {"w": {"drafts": {"approval": {"window": "30m"}}}}}`,
		`{"workflows": {"w": {"drafts": {"approval": {"mode": "delay"}}}}}`,
		`{"workflows": {"w": {"approval": {"window": "5m"}}}}`,
		`{"workflows": {"w": {"approval": {"mode": "delay"}}}}`,
		`{"workflows": {"w": {"approval": {"rule": [{"when": "brief.extra.ship == true", "mode": "wait"}]}}}}`,
		`{"workflows": {"w": {"approval": {"rule": [{"when": "classify.urgent == true", "mode": "wait"}]}}}}`,
		`{"workflows": {"w": {"only_when": "brief.extra.ship == true"}}}`,
	} {
		if err := loadErr(t, raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestAWorkflowsApprovalOverlayRefusesWhatNoManifestFixes(t *testing.T) {
	t.Parallel()
	for raw, msg := range map[string]string{
		`{"workflows": {"w": {"drafts": {"approval": {"mode": "sometimes"}}}}}`:                                                     "workflows.w.drafts.approval.mode",
		`{"workflows": {"w": {"drafts": {"approval": {"mode": "send", "window": "1h"}}}}}`:                                          "workflows.w.drafts.approval.window",
		`{"workflows": {"w": {"drafts": {"approval": {"rule": [{"when": "true"}]}}}}}`:                                              "workflows.w.drafts.approval.rule[0].mode",
		`{"workflows": {"w": {"drafts": {"approval": {"rule": [{"when": "true", "mode": "delay"}]}}}}}`:                             "workflows.w.drafts.approval.rule[0]",
		`{"workflows": {"w": {"drafts": {"approval": {"rule": [{"when": "brief.kind == \"request\"", "mode": "wait"}]}}}}}`:         "brief.kind",
		`{"workflows": {"w": {"drafts": {"approval": {"rule": [{"when": "brief.extra.ship == true", "mode": "wait"}]}}}}}`:          "brief.extra.ship",
		`{"workflows": {"w": {"approval": {"mode": "wait", "window": "5m"}}}}`:                                                      "workflows.w.approval.window",
		`{"workflows": {"w": {"approval": {"rule": [{"when": "true", "mode": "delay"}]}}}}`:                                         "workflows.w.approval.rule[0]",
		`{"workflows": {"w": {"approval": {"rule": [{"when": "brief.risk == \"low\"", "mode": "run"}]}}}}`:                          "brief.risk",
		`{"workflows": {"w": {"approval": {"rule": [{"when": "brief.extra.ship == true", "mode": "run"}]}}}}`:                       "brief.extra.ship",
		`{"workflows": {"w": {"approval": {"rule": [{"when": "trigger.event.data == \"go\"", "mode": "delay", "window": "1m"}]}}}}`: "trigger.event.data",
	} {
		if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want %q", raw, err, msg)
		}
	}
}
