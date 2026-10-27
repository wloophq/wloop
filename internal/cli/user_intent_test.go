package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
)

func TestAUserIntentGoesThroughTheWholeTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := warmReviewWorkspace()
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	userProfile(t, te, map[string]string{"intent=review.md": "Review well."})

	if code := te.run("ask-author", r7.URL(), "--topic", "the retry cap", "be", "brief"); code != 0 {
		t.Fatalf("ask-author: exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	round := askRound(t, m.Prompts[0].Text)
	if !strings.Contains(storedText(t, te, round), "About: the retry cap") {
		t.Fatalf("prompt %q", m.Prompts[0].Text)
	}
	handle := m.Prompts[0].Handle
	if _, err := te.Turns.Start(ctx, handle, round); err != nil {
		t.Fatal(err)
	}
	rec := record.Record{State: record.Asked, Summary: "asked what to ask", Resume: "post the question the user picks",
		Question: &record.Question{Kind: "free", Text: "Ask whether the retry cap is configurable?"}}
	if _, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: handle, Turn: round, Via: "mcp", Record: rec}); err != nil {
		t.Fatal(err)
	}
	b := te.bookOf(t, r7)
	if len(b.Turns) != 1 || b.Turns[0].State != dturn.Asked || b.Turns[0].Inputs["topic"] != "the retry cap" || b.Turns[0].Guidance != "be brief" {
		t.Fatalf("turns %+v", b.Turns)
	}
	asked := b.Turns[0]

	te.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	got, err := te.Turns.Answer(ctx, turns.AnswerRequest{Unit: r7.Unit(), Text: "yes, ask it", Source: dturn.Source("cli")})
	if err != nil || got.Route != dturn.Resumed {
		t.Fatalf("answer %+v %v\n%s", got, err, te.err)
	}
	b = te.bookOf(t, r7)
	if len(b.Turns) != 2 || b.Turns[1].Intent != "ask-author" || b.Turns[1].ResumeOf != asked.Seq || b.Turns[1].Inputs["topic"] != "the retry cap" {
		t.Fatalf("turns %+v", b.Turns)
	}
	if len(m.Prompts) != 2 || !strings.Contains(storedText(t, te, askRound(t, m.Prompts[1].Text)), "yes, ask it") {
		t.Fatalf("prompts %+v", m.Prompts)
	}

	t.Run("help lists it", func(t *testing.T) {
		te.out.Reset()
		if code := te.run("ask-author", "--help"); code != 0 || !strings.Contains(te.out.String(), " ask-author <url> [--topic <topic>] [guidance...]") {
			t.Fatalf("exit %d: %s", code, te.out)
		}
	})
	t.Run("the catalog serves it", func(t *testing.T) {
		res, err := client.New(te.Socket).Actions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(res.Specs, func(s api.ActionSpec) bool { return s.ID == "ask-author" }) {
			t.Fatalf("specs %+v", res.Specs)
		}
	})
	t.Run("profile check lists it", func(t *testing.T) {
		te.out.Reset()
		te.err.Reset()
		if code := te.run("profile", "check"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		out := te.out.String()
		lines := strings.Split(out, "\n")
		header := slices.Index(lines, "Intents")
		ask := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "ask-author") })
		block := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "review ") && strings.Contains(l, "intent=review.md") })
		if header < 0 || ask < 0 || block < 0 || header >= ask || ask >= block {
			t.Fatalf("want the Intents header (%d), then ask-author (%d), then the first block (%d):\n%s", header, ask, block, out)
		}
	})
	t.Run("profile show --show renders it", func(t *testing.T) {
		te.out.Reset()
		te.err.Reset()
		if code := te.run("profile", "show", "acme/api", "--show"); code != 0 {
			t.Fatalf("exit %d: %s%s", code, te.out, te.err)
		}
		if out := te.out.String(); !strings.Contains(out, "Ask the author of acme/api#") || !strings.Contains(out, "About: <topic>") {
			t.Fatalf("ask-author not rendered:\n%s", out)
		}
	})
}

var askPrompt = regexp.MustCompile("^.*? Run `[^`]* instructions ask-author --turn ([0-9a-f]{12}) \\S+` and follow what it prints\\.$")

func askRound(t *testing.T, prompt string) string {
	t.Helper()
	m := askPrompt.FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("not an ask-author prompt: %q", prompt)
	}
	return m[1]
}

func storedText(t *testing.T, te *testEnv, round string) string {
	t.Helper()
	got, ok, err := te.Store.Turn(context.Background(), round)
	if err != nil || !ok {
		t.Fatalf("turn %s: %v %v", round, ok, err)
	}
	return got.Text
}

func TestABrokenUserIntentsBlocksLeaveEveryOtherRoundAlone(t *testing.T) {
	t.Parallel()
	m := warmReviewWorkspace()
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	dir := userProfile(t, te, map[string]string{"intent=review.md": "Review well.", "intent=ask-author.md": "Ask kindly."})
	manifest := filepath.Join(dir, "intents", "ask-author", "intent.toml")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	broken := "profile = \"ask-author\"\n" + string(raw) + "\nunknown_key = 1\n"
	if err := os.WriteFile(manifest, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := te.loadedIntents().Broken["ask-author"]; !ok {
		t.Fatal("the manifest loads")
	}

	if code := te.run("review", r7.URL()); code != 0 {
		t.Fatalf("review: exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	te.out.Reset()
	te.err.Reset()
	if code := te.run("profile", "check"); code == 0 || !strings.Contains(te.err.String(), "unknown_key") {
		t.Fatalf("profile check: exit %d: %s", code, te.err)
	}
	te.out.Reset()
	te.err.Reset()
	code := te.run("profile", "show", "acme/api")
	if out := te.out.String() + te.err.String(); code == 0 || strings.Contains(out, "unknown intent") || !strings.Contains(out, "ask-author does not load") || !strings.Contains(out, "unknown_key") {
		t.Fatalf("doctor: exit %d:\n%s", code, out)
	}
}
