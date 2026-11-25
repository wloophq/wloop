package turn

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

func intentDef(t *testing.T, l *intentload.Loaded, name dintent.Name) dintent.Def {
	t.Helper()
	d, ok := l.Registry.Get(name)
	if !ok {
		t.Fatalf("no intent %s", name)
	}
	return d
}

func TestReviewAddressNudgeSourceWritesOff(t *testing.T) {
	l := bundled(t)
	for _, n := range []dintent.Name{dintent.Review, dintent.Address, dintent.Nudge} {
		if !intentDef(t, l, n).Session.NoSourceWrites {
			t.Errorf("%s: its sessions may write to GitHub; set [session] source_writes = false", n)
		}
	}
}

func TestReviewWorkflowRestartsOnFailedOrStaleDraft(t *testing.T) {
	l := bundled(t)
	for _, w := range l.Workflows {
		if w.Name != "review-draft-failed" {
			continue
		}
		for _, fact := range []string{"draft.failed", "draft.stale", "draft.failed_kind"} {
			if !slices.Contains(w.When.Reads(), fact) {
				t.Errorf("its when reads %v, not %s", w.When.Reads(), fact)
			}
		}
		if !slices.Equal(w.Key, []string{"draft.failed_ref"}) {
			t.Errorf("its key is %v: each failed or stale draft gets its own round", w.Key)
		}
		if len(w.Steps) != 1 || w.Steps[0].Use != "turn.start" || w.Steps[0].Args["intent"] != "review" {
			t.Errorf("it runs %+v, want one turn.start of review", w.Steps)
		}
		return
	}
	t.Fatal("no review-draft-failed workflow")
}

func TestReviewWorkflowRestartsOnAHeldDraftThePullRequestMovedPast(t *testing.T) {
	l := bundled(t)
	for _, w := range l.Workflows {
		if w.Name != "review-draft-stale" {
			continue
		}
		for _, fact := range []string{"draft.held_ref", "draft.held_kind", "draft.held_basis", "draft.held_stale", "pr.head", "pr.commits", "pr.open_turn", "pr.state"} {
			if !slices.Contains(w.When.Reads(), fact) {
				t.Errorf("its when reads %v, not %s", w.When.Reads(), fact)
			}
		}
		if !slices.Equal(w.Key, []string{"draft.held_ref"}) {
			t.Errorf("its key is %v: each held draft rev gets one round", w.Key)
		}
		if len(w.Steps) != 1 || w.Steps[0].Use != "turn.start" || w.Steps[0].Args["intent"] != "review" ||
			w.Steps[0].Args["due"] != "when" || w.Steps[0].Args["open_question"] != "replace_if_moved" {
			t.Errorf("it runs %+v, want one turn.start of review, due when, that replaces a moved question", w.Steps)
		}
		return
	}
	t.Fatal("no review-draft-stale workflow")
}

// bundledWorkflow is the bundled workflow named name.
func bundledWorkflow(t *testing.T, name string) workflow.Workflow {
	t.Helper()
	for _, w := range bundled(t).Workflows {
		if w.Name == name {
			return w
		}
	}
	t.Fatalf("no %s workflow", name)
	return workflow.Workflow{}
}

// heldReview are the facts of an open pull request at head c3 whose agent's review draft 5:1, read at c2, waits
// for the user while its turn asks, with edit applied.
func heldReview(edit func(workflow.Facts)) workflow.Facts {
	f := workflow.Facts{"pr.state": "OPEN", "pr.head": "c3", "pr.commits": []string{"c1", "c2", "c3"}, "pr.open_turn": "asked",
		"draft.held_ref": "5:1", "draft.held_kind": "review", "draft.held_basis": "c2", "draft.held_stale": false,
		"draft.failed": false, "draft.stale": false, "draft.failed_kind": "", "draft.failed_ref": ""}
	if edit != nil {
		edit(f)
	}
	return f
}

func TestReviewDraftStaleFiresOnlyWhenTheStoredHeadMovedPastAHeldReview(t *testing.T) {
	w := bundledWorkflow(t, "review-draft-stale")
	for _, c := range []struct {
		name string
		edit func(workflow.Facts)
		want bool
	}{
		{"basis behind the stored head", nil, true},
		{"refused as stale at the stored head", func(f workflow.Facts) { f["draft.held_basis"], f["draft.held_stale"] = "c3", true }, true},
		{"the question dismissed", func(f workflow.Facts) { f["pr.open_turn"] = "dismissed" }, true},
		{"no open turn", func(f workflow.Facts) { f["pr.open_turn"] = "" }, true},
		{"a turn sent", func(f workflow.Facts) { f["pr.open_turn"] = "sent" }, false},
		{"a turn started", func(f workflow.Facts) { f["pr.open_turn"] = "started" }, false},
		{"basis at the stored head", func(f workflow.Facts) { f["draft.held_basis"] = "c3" }, false},
		{"basis ahead of the stored head", func(f workflow.Facts) { f["draft.held_basis"] = "c4" }, false},
		{"merged", func(f workflow.Facts) { f["pr.state"] = "MERGED" }, false},
		{"closed, refused as stale", func(f workflow.Facts) { f["pr.state"], f["draft.held_stale"] = "CLOSED", true }, false},
		{"a held reply", func(f workflow.Facts) { f["draft.held_kind"] = "thread_reply" }, false},
		{"no held draft", func(f workflow.Facts) { f["draft.held_ref"], f["draft.held_kind"], f["draft.held_basis"] = "", "", "" }, false},
	} {
		got, err := w.When.Match(workflow.Vars{Facts: heldReview(c.edit)})
		if err != nil || got != c.want {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
}

func TestReviewDraftFailedPlansNothingOnAnEndedPullRequest(t *testing.T) {
	w := bundledWorkflow(t, "review-draft-failed")
	for state, want := range map[string]bool{"OPEN": true, "MERGED": false, "CLOSED": false} {
		f := heldReview(func(f workflow.Facts) {
			f["pr.state"], f["draft.failed"], f["draft.stale"], f["draft.failed_kind"], f["draft.failed_ref"] = state, true, true, "review", "5:1"
		})
		if got, err := w.When.Match(workflow.Vars{Facts: f}); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", state, got, err, want)
		}
	}
}

// writeCommands are the GitHub manifest's commands a session with
// source_writes = false may not run, each split in words.
func writeCommands(t *testing.T) [][]string {
	t.Helper()
	m, err := manifest.Load("../../plugins/github")
	if err != nil {
		t.Fatal(err)
	}
	src, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, c := range src.Session.WriteCommands {
		out = append(out, strings.Fields(c))
	}
	if len(out) == 0 {
		t.Fatal("the manifest declares no write command")
	}
	return out
}

// denied says that words start with one of patterns, where a * stands for
// one or more words, a word ending in * matches any word it prefixes, as
// -X* matches -XPOST, and a flag also matches its --flag=value form.
func denied(words []string, patterns [][]string) bool {
	var match func(w, p []string) bool
	match = func(w, p []string) bool {
		switch {
		case len(p) == 0:
			return true
		case len(w) == 0:
			return false
		case p[0] == "*":
			for i := 1; i <= len(w); i++ {
				if match(w[i:], p[1:]) {
					return true
				}
			}
			return false
		}
		prefix, wild := strings.CutSuffix(p[0], "*")
		word := w[0] == p[0] || strings.HasPrefix(w[0], p[0]+"=") || wild && strings.HasPrefix(w[0], prefix)
		return word && match(w[1:], p[1:])
	}
	for _, p := range patterns {
		if match(words, p) {
			return true
		}
	}
	return false
}

var inlineGh = regexp.MustCompile("`(gh [^`]*)`")

// ghCommands are the gh command lines text tells the agent to run: the
// lines of its fenced blocks and its inline code spans that start with gh.
func ghCommands(text string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
			continue
		}
		if fenced && strings.HasPrefix(trimmed, "gh ") {
			out = append(out, trimmed)
		}
	}
	for _, m := range inlineGh.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestDeniedMatchesPrefixWords(t *testing.T) {
	patterns := [][]string{{"gh", "api", "-X*"}, {"gh", "api", "*", "-f*"}}
	for _, c := range []string{"gh api -XPOST repos/acme/api/pulls/7/reviews", "gh api repos/acme/api/issues/7/comments -fbody=x", "gh api graphql -f query=x"} {
		if !denied(strings.Fields(c), patterns) {
			t.Errorf("%q is not denied", c)
		}
	}
	for _, c := range []string{"gh api repos/acme/api/pulls/7/comments --paginate", "gh api -x"} {
		if denied(strings.Fields(c), patterns) {
			t.Errorf("%q is denied", c)
		}
	}
}

func TestDeniedMatchesTheManifestsWriteCommands(t *testing.T) {
	writes := writeCommands(t)
	for _, c := range []string{"gh api repos/acme/api/pulls/7/reviews --method POST --input review.json", "gh pr comment 7 --body x",
		"gh api graphql -f query='{ viewer { login } }'", "gh api -X POST repos/acme/api/pulls/7/requested_reviewers", "gh pr review 7 --approve",
		"gh api repos/acme/api/pulls/7/comments/9/replies --raw-field=body=x"} {
		if !denied(strings.Fields(c), writes) {
			t.Errorf("%q is not denied", c)
		}
	}
	for _, c := range []string{"gh pr view 7 --json headRefOid", "gh api repos/acme/api/pulls/7/comments --paginate", "gh pr diff 7"} {
		if denied(strings.Fields(c), writes) {
			t.Errorf("%q is denied", c)
		}
	}
}

func TestInstructionsNameDraftTools(t *testing.T) {
	l := bundled(t)
	writes := writeCommands(t)
	drafting := map[dintent.Name]bool{dintent.Review: true, dintent.Address: true, dintent.Nudge: true}
	seen := 0
	for _, s := range Scenarios() {
		if !drafting[dintent.Name(s.Context.Intent)] {
			continue
		}
		seen++
		r, err := Render(s.Context, l, "")
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		for _, c := range ghCommands(r.Text) {
			if denied(strings.Fields(c), writes) {
				t.Errorf("%s: the instructions run %q, a write its session refuses", s.Name, c)
			}
		}
		for _, w := range []string{"resolveReviewThread", "mutation", "gh pr comment", "gh pr review", "requested_reviewers", "gh run rerun", "gh issue create", "file an issue"} {
			if strings.Contains(r.Text, w) {
				t.Errorf("%s: the instructions still write through %q", s.Name, w)
			}
		}
		drafts := !s.Context.Plain && !s.Context.NeedsTakeOver
		if got := strings.Contains(r.Text, "`github_post`"); got != drafts {
			t.Errorf("%s: names github_post %v, want %v", s.Name, got, drafts)
		}
	}
	if seen == 0 {
		t.Fatal("no review, address or nudge scenario")
	}
}

// probe is a draft of a GitHub kind a shipped rule decides on, its payload
// as proposed, and whether the rule sends it.
type probe struct {
	kind, payload string
	sends         bool
}

// shippedProbes leave an approval, a review whose event is absent or
// unknown, and a comment to the user, and send a review that comments or
// requests changes, a thread reply and a resolve.
var shippedProbes = []probe{
	{"review", `{"event": "APPROVE"}`, false},
	{"review", `{}`, false},
	{"review", ``, false},
	{"review", `{"event": "DISMISS"}`, false},
	{"review", `{"event": "COMMENT"}`, true},
	{"review", `{"event": "REQUEST_CHANGES"}`, true},
	{"comment", `{}`, false},
	{"thread_reply", `{}`, true},
	{"resolve", `{}`, true},
}

// addressProbes are what the address intent alone sends besides
// shippedProbes: a review request and the round's top-level comment.
var addressProbes = []probe{{"request_review", `{"reviewers": ["octocat"]}`, true}, {"comment", `{}`, true}}

// addressShipped is shippedProbes with addressProbes' kinds replaced by them.
func addressShipped() []probe {
	out := slices.DeleteFunc(slices.Clone(shippedProbes), func(p probe) bool {
		return slices.ContainsFunc(addressProbes, func(a probe) bool { return a.kind == p.kind })
	})
	return append(out, addressProbes...)
}

// kindSpec is the GitHub manifest's spec of kind, as the outbox decides a
// draft of it.
func kindSpec(t *testing.T, kind string) draft.KindSpec {
	t.Helper()
	spec, ok := builtin.DraftKinds.Lookup(string(unit.GitHub), kind)
	if !ok {
		t.Fatalf("github declares no draft kind %s", kind)
	}
	return spec
}

func TestShippedRulesAllowList(t *testing.T) {
	l := bundled(t)
	for n, probes := range map[dintent.Name][]probe{dintent.Review: shippedProbes, dintent.Address: addressShipped()} {
		p := intentDef(t, l, n).DraftApproval
		if p == nil {
			t.Fatalf("%s ships no draft approval", n)
		}
		for _, pr := range probes {
			spec := kindSpec(t, pr.kind)
			d := draft.Draft{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, Kind: draft.Kind(pr.kind), Delivery: draft.Delivery(spec.Delivery), Payload: []byte(pr.payload)}
			dec, err := p.Decide(d, draft.Subject{Kind: spec}, builtin.DeliveryPosts(spec.Delivery), time.Time{})
			if got := err == nil && dec.Approval.Releases(); got != pr.sends {
				t.Errorf("%s: a %s draft with payload %q sends %v, want %v (%+v, %v)", n, pr.kind, pr.payload, got, pr.sends, dec, err)
			}
		}
	}
}
