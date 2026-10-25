package builtin

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// workflowTable is the plan's workflow fact table: slot, then name:type pairs.
var workflowTable = map[string]string{
	"pr": "state:S merged_at:T closed_at:T finished_for:D draft:B head:S conflicting_for:D mine:B checks:S stalled:B age:D held:B adopted:B muted:B " +
		"has_workspace:B stale:B open_turn:S open_turn_seq:I orphan_answer_id:I review.action:S review.reason:S review.observed:B " +
		"first.due:B first.reason:S address.due:B address.reason:S address.reviewers:L address.checks:L address.conflict:B address.observed:B " +
		"last_interaction_at:T silent_days:I waits_on_user:B nudge_refusal:S fingerprint:S repo:S owner:S author:S visibility:S",
	"session": "state:S manager:S managed:B listed:B orphaned:B agent_state:S user_active:B user_typed_after_turn:B prompt_knowable:B " +
		"prompted_while_idle:B open_turn:S newest_state:S newest_age:D newest_intent:S closes_when_done:B idle_since:T " +
		"idle_for:D pr_state:S duplicate:B worktree_gone:B closed_by_manager:B transcript_gone:B process_gone:B fingerprint:S repo:S",
	"workspace": "role:S main:B pr:S pr_state:S pr_finished_for:D path_exists:B temporary:B duplicate:B dirty:I untracked:I " +
		"unpushed:I newest_turn_state:S open_turn:B pendency:L sessions:I manager:S last_activity:T quiet_for:D fingerprint:S repo:S",
	"trigger": "kind:S event.kind:S event.key:S event.data:S fired_at:T",
	"config":  "cleanup.idle:D cleanup.asked_idle:D cleanup.workspace_grace:D cleanup.manual_idle:D cleanup.stale_workspace:D nudge.silent_days:I",
}

var tableTypes = map[string]expr.Type{"B": expr.Bool, "I": expr.Int, "S": expr.String, "L": expr.List, "D": expr.Duration, "T": expr.Time}

func TestEveryWorkflowFactHasASlotAndAType(t *testing.T) {
	want := map[string]expr.Type{}
	for slot, row := range workflowTable {
		for _, f := range strings.Fields(row) {
			name, typ, _ := strings.Cut(f, ":")
			want[slot+"."+name] = tableTypes[typ]
		}
	}
	got := map[string]expr.Type{}
	for _, f := range Facts {
		if !slices.Contains(f.Slots, WorkflowSlot) {
			if f.Slot != "" {
				t.Errorf("%s names slot %s but is no workflow fact", f.Name, f.Slot)
			}
			continue
		}
		if len(f.Slots) != 1 {
			t.Errorf("%s is a workflow fact and also of %v", f.Name, f.Slots)
		}
		if !strings.HasPrefix(f.Name, f.Slot+".") || !slices.Contains(WorkflowSlots, f.Slot) {
			t.Errorf("%s: slot %q", f.Name, f.Slot)
		}
		if typ, ok := FactType(f.Name); !ok || typ != f.Type {
			t.Errorf("FactType(%s) = %v, %v; want %v", f.Name, typ, ok, f.Type)
		}
		got[f.Name] = f.Type
	}
	for name, typ := range want {
		if got[name] != typ {
			t.Errorf("%s: %v, want %v", name, got[name], typ)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s is not in the plan's fact table", name)
		}
	}
	if _, ok := FactType("pr.slug"); ok {
		t.Error("FactType reads intent facts")
	}
	for _, s := range Slots {
		if !slices.Contains(WorkflowSlots, s) {
			t.Errorf("provider slot %s has no facts", s)
		}
	}
}

func TestWorkflowEnvIsOneSlotWithTriggerAndConfig(t *testing.T) {
	env := WorkflowEnv(Slots[unit.Session])
	for name := range env {
		slot, _, _ := strings.Cut(name, ".")
		if slot != "session" && slot != "trigger" && slot != "config" {
			t.Errorf("session env holds %s", name)
		}
	}
	for _, name := range []string{"session.idle_for", "trigger.event.kind", "config.cleanup.idle"} {
		if _, ok := env[name]; !ok {
			t.Errorf("session env lacks %s", name)
		}
	}
	if _, err := expr.CompileWorkflow(`session.pr_state in ["merged", "closed"] && session.idle_for > config.cleanup.idle`, env); err != nil {
		t.Error(err)
	}
}

func TestAnUnknownSlotFactSuggests(t *testing.T) {
	cases := map[string]string{
		`session.idle_fr > duration("2h")`: "unknown field session.idle_fr; did you mean session.idle_for?",
		`pr.state == "open"`:               "unknown field pr.state",
	}
	for src, msg := range cases {
		_, err := expr.CompileWorkflow(src, WorkflowEnv("session"))
		var e *expr.Error
		if !errors.As(err, &e) || e.Col != 1 || e.Msg != msg {
			t.Errorf("%q: %v, want %q", src, err, msg)
		}
	}
}
