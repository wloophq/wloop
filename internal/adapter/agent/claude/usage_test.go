package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/usage"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
)

var (
	windowFrom = time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	windowTo   = time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "transcripts", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func copyFixture(t *testing.T, name, to string) {
	t.Helper()
	b, err := os.ReadFile(fixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readUsageOf(t *testing.T, u Usage, turn usage.Turn) []dusage.Model {
	t.Helper()
	turn.From, turn.To = windowFrom, windowTo
	got, err := u.Usage(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var streamed = dusage.Model{
	Model: "opus-fixture", Messages: 2,
	Tokens: dusage.Tokens{Input: 12, Output: 127, CacheRead: 400, CacheWrite: 20},
	First:  time.Date(2026, 10, 18, 10, 0, 3, 0, time.UTC),
	Last:   time.Date(2026, 10, 18, 10, 0, 5, 0, time.UTC),
}

func sameModels(t *testing.T, got, want []dusage.Model) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d models %+v, want %+v", len(got), got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Model != w.Model || g.Messages != w.Messages || g.Tokens != w.Tokens || !g.First.Equal(w.First) || !g.Last.Equal(w.Last) || g.CostUSD != nil {
			t.Fatalf("model %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestUsageKeepsTheLargestLineOfAMessage(t *testing.T) {
	got := readUsageOf(t, Usage{}, usage.Turn{AgentSession: "s1", Transcript: fixture(t, "streamed.jsonl")})
	sameModels(t, got, []dusage.Model{streamed})
}

func TestUsageIncludesSubagentsInTheWindow(t *testing.T) {
	got := readUsageOf(t, Usage{}, usage.Turn{AgentSession: "s2", Transcript: fixture(t, "window.jsonl")})
	sameModels(t, got, []dusage.Model{
		{
			Model: "haiku-fixture", Messages: 1, Tokens: dusage.Tokens{Input: 4, Output: 9, CacheWrite: 11},
			First: time.Date(2026, 10, 18, 10, 40, 0, 0, time.UTC), Last: time.Date(2026, 10, 18, 10, 40, 0, 0, time.UTC),
		},
		{
			Model: "opus-fixture", Messages: 1, Tokens: dusage.Tokens{Input: 3, Output: 30, CacheRead: 50, CacheWrite: 5},
			First: time.Date(2026, 10, 18, 10, 30, 0, 0, time.UTC), Last: time.Date(2026, 10, 18, 10, 30, 0, 0, time.UTC),
		},
	})
}

func TestBackfillDerivesThePathOfAnOldSession(t *testing.T) {
	tmp := t.TempDir()
	copyFixture(t, "streamed.jsonl", filepath.Join(tmp, "h", "projects", "-Users-me-orca-workspaces-x-rv-a-b", "id.jsonl"))
	got := readUsageOf(t, Usage{UserHome: filepath.Join(tmp, "user")}, usage.Turn{
		AgentSession: "id", Home: filepath.Join(tmp, "h"), Worktree: "/Users/me/orca/workspaces/x/rv-a.b",
	})
	sameModels(t, got, []dusage.Model{streamed})
}

func TestEmptyHomeUsesTheDefault(t *testing.T) {
	user := t.TempDir()
	copyFixture(t, "streamed.jsonl", filepath.Join(DefaultHome(user), "projects", "-w-t", "id.jsonl"))
	got := readUsageOf(t, Usage{UserHome: user}, usage.Turn{AgentSession: "id", Worktree: "/w/t"})
	sameModels(t, got, []dusage.Model{streamed})
}

func TestMissingTranscriptIsErrNoTranscript(t *testing.T) {
	for name, turn := range map[string]usage.Turn{
		"stored":  {AgentSession: "id", Transcript: filepath.Join(t.TempDir(), "gone.jsonl")},
		"derived": {AgentSession: "id", Home: t.TempDir(), Worktree: "/w/t"},
	} {
		turn.From, turn.To = windowFrom, windowTo
		if _, err := (Usage{UserHome: t.TempDir()}).Usage(context.Background(), turn); !errors.Is(err, usage.ErrNoTranscript) {
			t.Errorf("%s: err = %v, want ErrNoTranscript", name, err)
		}
	}
}

func assistantAt(id, model, at string, output int64) string {
	idField := ""
	if id != "" {
		idField = `"id":"` + id + `",`
	}
	return `{"type":"assistant","timestamp":"` + at + `","message":{` + idField + `"model":"` + model +
		`","content":[],"usage":{"input_tokens":1,"output_tokens":` + strconv.FormatInt(output, 10) +
		`,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`
}

func TestUsageEdgesOfTheWindowAndOfAMessage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lines    []string
		messages int
		output   int64
	}{
		{"an entry at From counts", []string{assistantAt("a", "opus-fixture", "2026-10-18T10:00:00Z", 3)}, 1, 3},
		{"an entry at To counts", []string{assistantAt("a", "opus-fixture", "2026-10-18T11:00:00Z", 3)}, 1, 3},
		{"lines without an id each count", []string{
			assistantAt("", "opus-fixture", "2026-10-18T10:10:00Z", 4),
			assistantAt("", "opus-fixture", "2026-10-18T10:10:00Z", 4),
		}, 2, 8},
		{"a message streamed across To keeps its largest line inside", []string{
			assistantAt("s", "opus-fixture", "2026-10-18T10:59:59Z", 16),
			assistantAt("s", "opus-fixture", "2026-10-18T11:00:01Z", 32),
		}, 1, 16},
		{"a synthetic entry is not a message", []string{
			assistantAt("a", "opus-fixture", "2026-10-18T10:10:00Z", 5),
			assistantAt("x", "<synthetic>", "2026-10-18T10:20:00Z", 0),
		}, 1, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(tc.lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got := readUsageOf(t, Usage{}, usage.Turn{AgentSession: "s", Transcript: path})
			if len(got) != 1 || got[0].Model != "opus-fixture" || got[0].Messages != tc.messages || got[0].Tokens.Output != tc.output {
				t.Fatalf("got %+v, want %d messages with %d output tokens", got, tc.messages, tc.output)
			}
		})
	}
}

func TestAGoneStoredTranscriptFallsBackToTheDerivedPath(t *testing.T) {
	home := t.TempDir()
	copyFixture(t, "streamed.jsonl", filepath.Join(home, "projects", "-w-t", "id.jsonl"))
	got := readUsageOf(t, Usage{}, usage.Turn{
		AgentSession: "id", Transcript: filepath.Join(t.TempDir(), "moved.jsonl"), Home: home, Worktree: "/w/t",
	})
	sameModels(t, got, []dusage.Model{streamed})
}

func TestTheSlugReplacesEachCharacterOnce(t *testing.T) {
	if got := projectSlug("/Users/zé/rv-a.b"); got != "-Users-z--rv-a-b" {
		t.Fatalf("projectSlug = %q", got)
	}
}
