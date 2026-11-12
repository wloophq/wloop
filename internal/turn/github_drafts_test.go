package turn

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
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
	t.Skip("T9: the workflow reads the draft.failed and draft.stale unit facts T9 declares; it ships with them")
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
		proposes := !s.Context.Plain && !s.Context.NeedsTakeOver
		if got := strings.Contains(r.Text, "`github_draft`"); got != proposes {
			t.Errorf("%s: names github_draft %v, want %v", s.Name, got, proposes)
		}
	}
	if seen == 0 {
		t.Fatal("no review, address or nudge scenario")
	}
}

// probe is a draft a shipped rule decides on, and what it must decide.
type probe struct {
	delivery, event string
	posts           bool
	want            draft.ApprovalMode
}

// shippedProbes leave an approval, a review whose event is absent or
// unknown, and a comment to the user, and send a thread reply and a
// resolve.
var shippedProbes = []probe{
	{delivery: "review", event: "APPROVE", posts: true, want: draft.Wait},
	{delivery: "review", posts: true, want: draft.Wait},
	{delivery: "review", event: "DISMISS", posts: true, want: draft.Wait},
	{delivery: "comment", posts: true, want: draft.Wait},
	{delivery: "reply", posts: true, want: draft.Send},
	{delivery: "resolve", want: draft.Send},
}

func TestShippedRulesAllowList(t *testing.T) {
	l := bundled(t)
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for _, n := range []dintent.Name{dintent.Review, dintent.Address} {
		p := intentDef(t, l, n).DraftApproval
		if p == nil {
			t.Fatalf("%s ships no draft approval", n)
		}
		for _, pr := range shippedProbes {
			d := draft.Draft{Delivery: draft.Delivery(pr.delivery)}
			if pr.event != "" {
				d.Payload, _ = json.Marshal(map[string]string{"event": pr.event})
			}
			got, err := p.Decide(d, draft.Subject{Slot: "pr"}, pr.posts, now)
			if err != nil {
				t.Fatalf("%s %+v: %v", n, pr, err)
			}
			if got.Approval.Mode != pr.want {
				t.Errorf("%s: a %s draft with event %q is %s, want %s", n, pr.delivery, pr.event, got.Approval.Mode, pr.want)
			}
		}
	}
}
