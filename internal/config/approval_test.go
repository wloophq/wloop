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
