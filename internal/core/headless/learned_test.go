package headless_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/domain/record"
)

func TestTheAnswersLearnedIsTheRecords(t *testing.T) {
	long := strings.Repeat("a", record.LearnedMax+40)
	for _, c := range []struct{ name, log, want string }{
		{"kept", "the date is Friday, set by the owner", "the date is Friday, set by the owner"},
		{"cut to the limit", long, long[:record.LearnedMax]},
		{"empty", "", ""},
		{"plain text", "line one\r\nline two\u202e\u2028line three\x00", "line one\nline two\nline three"},
	} {
		raw, err := json.Marshal(map[string]any{"record": done, "drafts": []any{}, "learned": c.log})
		if err != nil {
			t.Fatal(err)
		}
		r := newRig(t, scripted{json: string(raw)})
		if _, err := r.svc.Run(context.Background(), headless.Request{Unit: thread}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		b := r.book(t, thread)
		if len(b.Records) != 1 || b.Records[0].Record.Learned != c.want {
			t.Errorf("%s: records %+v", c.name, b.Records)
		}
	}
}

func TestTheSchemaBoundsLearnedAsTheRecordDoes(t *testing.T) {
	var s struct {
		Properties map[string]struct {
			MaxLength int `json:"maxLength"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(headless.Schema()), &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Properties["learned"].MaxLength; got != record.LearnedMax {
		t.Fatalf("learned maxLength %d, want %d", got, record.LearnedMax)
	}
}
