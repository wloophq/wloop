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
	if c := loadRaw(t, `turns = {per_day = 3}`); time.Duration(c.Drafts.RepeatAfter) != 24*time.Hour {
		t.Fatalf("drafts.repeat_after left out lost its default: %v", time.Duration(c.Drafts.RepeatAfter))
	}
	if c := loadRaw(t, `drafts = {repeat_after = "2d"}`); time.Duration(c.Drafts.RepeatAfter) != 48*time.Hour {
		t.Fatalf("%v", time.Duration(c.Drafts.RepeatAfter))
	}
}

func TestDraftsRepeatAfterMustBePositive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`drafts = {repeat_after = "0s"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, tools()); err == nil || !strings.Contains(err.Error(), "drafts.repeat_after") {
		t.Fatalf("err = %v", err)
	}
}

func TestPluginDraftApprovalRefusesABadApproval(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown mode":        `plugins.github = {gates = {approval = {mode = "sometimes"}}}`,
		"window without time": `plugins.github = {gates = {approval = {mode = "delay"}}}`,
		"time without window": `plugins.github = {gates = {approval = {mode = "wait", window = "1h"}}}`,
		"not an object":       `plugins.github = {gates = {approval = 3}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), "plugins.github.gates.approval") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestAWorkflowsApprovalLoadsAsAnOverlay(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`workflows = {w = {gates = {approval = {rule = [{when = "slack.conversation == \"dm\"", mode = "send"}]}}}}`,
		`workflows = {w = {gates = {approval = {rule = []}}}}`,
		`workflows = {w = {gates = {approval = {window = "30m"}}}}`,
		`workflows = {w = {gates = {approval = {mode = "delay"}}}}`,
		`workflows = {w = {gates = {approval = {mode = "send"}}}}`,
		`workflows = {w = {gates = {approval = {rule = [{when = "gate.kind == \"plan\"", mode = "run"}]}}}}`,
		`workflows = {w = {gates = {approval = {rule = [{when = "draft.delivery == \"message\"", mode = "dry_run"}]}}}}`,
		`workflows = {w = {gates = {approval = {rule = [{when = "brief.kind == \"request\"", mode = "wait"}]}}}}`,
		`workflows = {w = {gates = {approval = {rule = [{when = "brief.extra.ship == true", mode = "wait"}]}}}}`,
		`workflows = {w = {gates = {approval = {rule = [{when = "classify.urgent == true", mode = "wait"}]}}}}`,
		`workflows = {w = {only_when = "brief.extra.ship == true"}}`,
	} {
		if err := loadErr(t, raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestAWorkflowsApprovalOverlayRefusesWhatNoManifestFixes(t *testing.T) {
	t.Parallel()
	for raw, msg := range map[string]string{
		`workflows = {w = {gates = {approval = {mode = "sometimes"}}}}`:                                                              "workflows.w.gates.approval.mode",
		`workflows = {w = {gates = {approval = {mode = "send", window = "1h"}}}}`:                                                    "workflows.w.gates.approval.window",
		`workflows = {w = {gates = {approval = {mode = "wait", window = "5m"}}}}`:                                                    "workflows.w.gates.approval.window",
		`workflows = {w = {gates = {approval = {rule = [{when = "true"}]}}}}`:                                                        "workflows.w.gates.approval.rule[0].mode",
		`workflows = {w = {gates = {approval = {rule = [{when = "true", mode = "delay"}]}}}}`:                                        "workflows.w.gates.approval.rule[0]",
		`workflows = {w = {gates = {approval = {rule = [{when = "gate.kind == \"plan\"", mode = "dry_run"}]}}}}`:                     "dry_run, which applies to draft gates only",
		`workflows = {w = {gates = {approval = {rule = [{when = "brief.risk == \"low\"", mode = "run"}]}}}}`:                         "brief.risk",
		`workflows = {w = {gates = {approval = {rule = [{when = "brief.extra.ship == true", mode = "send"}]}}}}`:                     "brief.extra.ship",
		`workflows = {w = {gates = {approval = {rule = [{when = "trigger.event.data == \"go\"", mode = "delay", window = "1m"}]}}}}`: "trigger.event.data",
	} {
		if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want %q", raw, err, msg)
		}
	}
}
