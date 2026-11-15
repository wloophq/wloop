package intent_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/turn"
)

var outboxNow = time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)

// bundledWorkflow is the bundled workflow name.
func bundledWorkflow(t *testing.T, name string) workflow.Workflow {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == name })
	if i < 0 {
		t.Fatalf("no bundled workflow %s", name)
	}
	return l.Workflows[i]
}

// outboxVars are the zero of every fact w's withdraw env holds, under the default configuration, then set.
func outboxVars(w workflow.Workflow, set map[string]any) workflow.Vars {
	v := workflow.Vars{Facts: workflow.Facts{}, Trigger: workflow.Facts{}, Config: workflow.Facts{}, Now: outboxNow}
	for name, typ := range builtin.WithdrawEnv(w.Slot) {
		var zero any
		switch typ {
		case expr.Bool:
			zero = false
		case expr.Int:
			zero = 0
		case expr.String:
			zero = ""
		case expr.List:
			zero = []string{}
		case expr.Duration:
			zero = time.Duration(0)
		case expr.Time:
			zero = time.Time{}
		}
		switch {
		case strings.HasPrefix(name, "config."):
			v.Config[name] = zero
		case strings.HasPrefix(name, "trigger."):
			v.Trigger[name] = zero
		default:
			v.Facts[name] = zero
		}
	}
	d := config.Defaults()
	v.Config["config.drafts.repeat_after"] = time.Duration(d.Drafts.RepeatAfter)
	v.Config["config.drafts.stale_after_days"] = d.Drafts.StaleAfterDays
	for k, val := range set {
		if strings.HasPrefix(k, "trigger.") {
			v.Trigger[k] = val
			continue
		}
		v.Facts[k] = val
	}
	return v
}

func matches(t *testing.T, p workflow.Predicate, v workflow.Vars) bool {
	t.Helper()
	ok, err := p.Match(v)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// withdrawn is the reason w's withdraw rules withdraw a draft on v; "" when none does.
func withdrawn(t *testing.T, w workflow.Workflow, v workflow.Vars) string {
	t.Helper()
	for _, r := range w.Withdrawals() {
		if matches(t, r.When, v) {
			return r.Reason
		}
	}
	return ""
}

// stalled are the facts of a task a bundled Slack nudge is due on, waiting on Bruno.
func stalled(set map[string]any) map[string]any {
	out := map[string]any{"task.unit_providers": []string{"slack"}, "task.owed_by": "slack:U000BRUNO", "task.check_on": outboxNow.Add(-time.Hour),
		"draft.person_waited": true, "draft.items_owed": 1}
	for k, v := range set {
		out[k] = v
	}
	return out
}

func TestTheThreadNudgeWaitsWhileTheNewestMessageIsToYouAndTheDMNudgeDoesNot(t *testing.T) {
	toYou := stalled(map[string]any{builtin.LinkSignalPrefix + "last_to_me": true})
	for name, want := range map[string]bool{"slack-stalled-nudge": false, "slack-stalled-nudge-dm": true} {
		w := bundledWorkflow(t, name)
		if !matches(t, w.When, outboxVars(w, stalled(nil))) {
			t.Fatalf("%s: a quiet task waiting on Bruno is not nudged", name)
		}
		if got := matches(t, w.When, outboxVars(w, toYou)); got != want {
			t.Errorf("%s: with the thread's newest message to you, matches %v, want %v", name, got, want)
		}
	}
}

func TestTheThreadNudgeHoldsForTheRepeatIntervalAfterThePersonMoved(t *testing.T) {
	w := bundledWorkflow(t, "slack-stalled-nudge")
	for _, c := range []struct {
		what string
		set  map[string]any
		want bool
	}{
		{"nothing since the ask", nil, false},
		{"they replied two days ago", map[string]any{"draft.people_posted_at": outboxNow.Add(-48 * time.Hour)}, true},
		{"they said next week, then three weeks of silence", map[string]any{"draft.people_posted_at": outboxNow.Add(-21 * 24 * time.Hour)}, false},
		{"they reacted yesterday", map[string]any{"draft.people_reacted_at": outboxNow.Add(-24 * time.Hour)}, true},
		{"it mentions a team named by text", map[string]any{"draft.people_unmentionable": 1}, true},
		{"its newest ask is a week old", map[string]any{"draft.items_age_days": 5}, false},
		{"its newest ask is five weeks old", map[string]any{"draft.items_age_days": 25}, true},
	} {
		if got := withdrawn(t, w, outboxVars(w, stalled(c.set))) != ""; got != c.want {
			t.Errorf("%s: withdrawn %v, want %v", c.what, got, c.want)
		}
	}
}

func TestTheDMNudgeReadsNothingOfTheThreadItsPersonCannotRead(t *testing.T) {
	w := bundledWorkflow(t, "slack-stalled-nudge-dm")
	for _, set := range []map[string]any{{"draft.people_posted_at": outboxNow.Add(-time.Hour)}, {"draft.people_reacted_at": outboxNow.Add(-time.Hour)}} {
		if r := withdrawn(t, w, outboxVars(w, stalled(set))); r != "" {
			t.Errorf("%v withdrew the DM nudge as %s", set, r)
		}
	}
	if r := withdrawn(t, w, outboxVars(w, stalled(map[string]any{"draft.items_age_days": 25}))); r == "" {
		t.Error("a DM nudge over a five-week-old ask stays")
	}
}

func TestStaleAfterDaysZeroNeverWithdrawsANudgeForItsAge(t *testing.T) {
	w := bundledWorkflow(t, "slack-stalled-nudge")
	v := outboxVars(w, stalled(map[string]any{"draft.items_age_days": 200}))
	v.Config["config.drafts.stale_after_days"] = 0
	if r := withdrawn(t, w, v); r != "" {
		t.Fatalf("withdrawn %s with stale_after_days = 0", r)
	}
}

// resolvedThread are the facts of a thread you started whose items all resolved.
func resolvedThread(set map[string]any) map[string]any {
	out := map[string]any{"slack.started_by_me": true, "slack.items_resolved": 2, "draft.unit_open": true, "slack.extracted": true}
	for k, v := range set {
		out[k] = v
	}
	return out
}

func TestTheBundledClosureClosesWithoutAMessageByDefault(t *testing.T) {
	message, quiet := bundledWorkflow(t, "slack-closure"), bundledWorkflow(t, "slack-closure-quiet")
	if !message.Off || quiet.Off {
		t.Fatalf("slack-closure off %v, slack-closure-quiet off %v; want the message off and the quiet closure on", message.Off, quiet.Off)
	}
	if len(quiet.Steps) != 1 || quiet.Steps[0].Use != builtin.StepEffectsApply {
		t.Fatalf("slack-closure-quiet steps %+v, want effects.apply", quiet.Steps)
	}
	if quiet.Approval.Mode == workflow.ApprovalWait || len(quiet.Approval.Rules) > 0 {
		t.Fatalf("slack-closure-quiet's plan %+v, want it to run under the default policy, with no approval", quiet.Approval)
	}
	for _, c := range []struct {
		what           string
		set            map[string]any
		quiet, message bool
	}{
		{"the items resolved", nil, true, true},
		{"you reacted to the last answer", map[string]any{"slack.reacted_to_last": true}, true, false},
		{"an item resolved with no answer", map[string]any{"slack.items_resolved_unanswered": 1}, true, false},
		{"the newest message is to you", map[string]any{"slack.last_to_me": true}, false, false},
		{"a newer message waits for the extraction", map[string]any{"slack.extracted": false}, false, true},
	} {
		if got := matches(t, quiet.When, outboxVars(quiet, resolvedThread(c.set))); got != c.quiet {
			t.Errorf("%s: the quiet closure matches %v, want %v", c.what, got, c.quiet)
		}
		if got := matches(t, message.When, outboxVars(message, resolvedThread(c.set))); got != c.message {
			t.Errorf("%s: the closure message matches %v, want %v", c.what, got, c.message)
		}
	}
	if r := withdrawn(t, message, outboxVars(message, resolvedThread(map[string]any{"slack.reacted_to_last": true}))); r == "" {
		t.Error("a held closure message stays after you reacted to the last answer")
	}
}

// unowned are the facts of a tracked thread with an item nobody owned for two working days.
func unowned(set map[string]any) map[string]any {
	out := map[string]any{"slack.live_task": true, "slack.items_unowned": 1, "slack.items_unowned_age_days": 2}
	for k, v := range set {
		out[k] = v
	}
	return out
}

func TestTheBundledOwnerQuestionSparesYourOwnThreadAndAThreadWaitingOnYou(t *testing.T) {
	w := bundledWorkflow(t, "slack-ask-owner")
	for _, c := range []struct {
		what string
		set  map[string]any
		want bool
	}{
		{"someone else's quiet thread", nil, true},
		{"you started the thread", map[string]any{"slack.started_by_me": true}, false},
		{"you wrote its newest message", map[string]any{"slack.last_by_me": true}, false},
		{"its newest message is to you", map[string]any{"slack.last_to_me": true}, false},
	} {
		if got := matches(t, w.When, outboxVars(w, unowned(c.set))); got != c.want {
			t.Errorf("%s: matches %v, want %v", c.what, got, c.want)
		}
	}
	if r := withdrawn(t, w, outboxVars(w, unowned(map[string]any{"draft.items_age_days": 30}))); r == "" {
		t.Error("an owner question on a six-week-old item stays")
	}
}

func TestSayingATaskIsNotYoursDraftsNothingByDefault(t *testing.T) {
	w := bundledWorkflow(t, "task-not-mine")
	if !w.Off {
		t.Fatal("task-not-mine ships on")
	}
	if !matches(t, w.When, outboxVars(w, map[string]any{"trigger.event.kind": "verb_not-mine"})) {
		t.Error("turned on, it does not redirect when you say a task is not yours")
	}
}

func TestTheBundledTaskClosesRunWithNoApprovalOnceTheExtractionCaughtUp(t *testing.T) {
	for name, set := range map[string]map[string]any{
		"slack-done-replied": {builtin.LinkSignalPrefix + "replied_last": true},
		"slack-done-reacted": {builtin.LinkSignalPrefix + "asked_reacted": true},
		"slack-done-quiet":   {"task.link_ended": true},
	} {
		w := bundledWorkflow(t, name)
		if w.Approval.Mode == workflow.ApprovalWait || len(w.Approval.Rules) > 0 {
			t.Errorf("%s: plan %+v, want it to run under the default policy, with no approval", name, w.Approval)
		}
		facts := map[string]any{"task.prop.kind": "thread", "task.items_open": 1, "task.category": "waiting", "task.units": []string{"slack:a"}}
		for k, v := range set {
			facts[k] = v
		}
		if !matches(t, w.When, outboxVars(w, facts)) {
			t.Errorf("%s: does not match a task its thread shows done", name)
		}
		facts[builtin.LinkSignalPrefix+"extraction_pending"] = true
		if matches(t, w.When, outboxVars(w, facts)) {
			t.Errorf("%s: closes the task while its thread's newest message waits for the extraction", name)
		}
	}
}
