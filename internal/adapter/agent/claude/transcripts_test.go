package claude

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
)

func TestTranscriptPagesTheAgentTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sess-1.jsonl")
	toolOnly := `{"type":"assistant","timestamp":"2026-10-21T12:00:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`
	writeTranscript(t, path, slices.Insert(slices.Clone(transcriptLines), 2, toolOnly)...)
	stored := map[string]string{"sess-1": path}
	tr := Transcripts{Transcript: func(_ context.Context, id string) (string, string, error) {
		if p, ok := stored[id]; ok {
			return p, Agent{}.Name(), nil
		}
		return "", "", nil
	}}
	ctx := context.Background()
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	all := []turns.TranscriptLine{
		{Role: "user", Text: "/review https://example.test/acme/api/pull/6", At: at("2026-10-21T12:00:00Z")},
		{Role: "assistant", Text: "user", At: at("2026-10-21T12:00:05Z")},
		{Role: "user", Text: "and the tests?", At: at("2026-10-21T12:30:00Z")},
		{Role: "user", Text: "also the migration", At: at("2026-10-21T13:00:00Z")},
		{Role: "assistant", Text: "done", At: at("2026-10-21T13:01:00Z")},
	}
	for _, c := range []struct {
		from, limit int
		want        []turns.TranscriptLine
	}{
		{0, 10, all},
		{1, 2, all[1:3]},
		{4, 5, all[4:]},
		{9, 5, nil},
	} {
		got, total, err := tr.Page(ctx, "sess-1", c.from, c.limit)
		if err != nil || total != len(all) || !slices.Equal(got, c.want) {
			t.Errorf("from %d limit %d: %+v total %d %v, want %+v", c.from, c.limit, got, total, err, c.want)
		}
	}
	if got, total, err := tr.Page(ctx, "sess-unknown", 0, 10); err != nil || total != 0 || len(got) != 0 {
		t.Errorf("an unknown session: %+v %d %v", got, total, err)
	}
	stored["sess-2"] = filepath.Join(t.TempDir(), "gone.jsonl")
	if got, total, err := tr.Page(ctx, "sess-2", 0, 10); err != nil || total != 0 || len(got) != 0 {
		t.Errorf("a transcript gone from disk: %+v %d %v", got, total, err)
	}
}
