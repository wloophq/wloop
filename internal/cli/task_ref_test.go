package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTaskRefParses(t *testing.T) {
	t.Parallel()
	want := unit.Unit{Provider: unit.Task, Key: "t-3f9a2c4b5d6e7f80"}
	got, err := resolveTarget(context.Background(), "task:t-3f9a2c4b5d6e7f80", nil, nil, pluginCodecs())
	if err != nil || got != want {
		t.Errorf("task:<id> = %v %v, want %v", got, err, want)
	}
	if got, ok, err := explicitTask("task:t-3f9a2c4b5d6e7f80"); !ok || err != nil || got != want {
		t.Errorf("explicitTask = %v %v %v", got, ok, err)
	}
}

func TestEntryRefIsRefusedWithItsTaskForm(t *testing.T) {
	t.Parallel()
	_, err := resolveTarget(context.Background(), "entry:e-3f9a2c4b5d6e7f80", []unit.ProviderID{"github", "slack"}, nil, pluginCodecs())
	if err == nil || !strings.Contains(err.Error(), "task:t-3f9a2c4b5d6e7f80") {
		t.Fatalf("entry:<id> resolved or was refused without its task form: %v", err)
	}
	if _, ok, err := explicitTask("entry:e-3f9a2c4b5d6e7f80"); ok || err == nil || !strings.Contains(err.Error(), "task:t-3f9a2c4b5d6e7f80") {
		t.Fatalf("explicitTask(entry:<id>) = %v %v", ok, err)
	}
}
