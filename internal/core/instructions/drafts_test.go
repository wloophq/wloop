package instructions

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/record"
)

func TestStaleDraftPointerOnly(t *testing.T) {
	ds := []draft.Draft{
		{ID: 42, Kind: "review", State: draft.Failed, Basis: "c1", Text: "SECRET body", Original: "SECRET original", Payload: json.RawMessage(`{"event":"SECRET"}`),
			Error: "SECRET 422", Why: "SECRET why"},
		{ID: 43, Kind: "thread_reply", State: draft.Held, Text: "SECRET reply"},
	}
	got := Pointers(ds, "c2")
	want := []Draft{{ID: 42, Kind: "review", State: "failed", Basis: "c1", CurrentBasis: "c2", Stale: true}, {ID: 43, Kind: "thread_reply", State: "held", CurrentBasis: "c2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v, want %+v", got, want)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("a pointer carries the draft's words: %s", raw)
	}
	if fresh := Pointers(ds[:1], "c1"); fresh[0].Stale {
		t.Fatalf("a draft at the current basis: %+v", fresh)
	}
	b := book()
	b.Records[2].Record.Question.Options[0].Draft = &record.DraftRef{ID: 42, Rev: 3}
	r := ResumeOf(b, 3, "", "", "")
	if d := r.Question.Options[0].Draft; d == nil || *d != (record.DraftRef{ID: 42, Rev: 3}) {
		t.Fatalf("the option's pointer: %+v", r.Question.Options)
	}
}
