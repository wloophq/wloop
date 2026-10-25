package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestTurnEndHTTPReportsLedger(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	l := &turnstest.Ledger{Refuse: map[int]string{2: "item: unknown \"i9\""}, Propose: map[int]bool{1: true}}
	svc.Items = l
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	h := s.Handler()
	if rec := agentCall(t, h, "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"review"}`); rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	rec := `{"state":"done","summary":"reviewed","outcome":{"verdict":"COMMENT","url":"https://github.com/acme/api/pull/7#pullrequestreview-1"},"ledger":[
		{"op":"resolve","item":"i1","anchor":{"ref":"review:1"},"resolution":"done"},
		{"op":"resolve","item":"i2","anchor":{"ref":"review:1"},"resolution":"done"},
		{"op":"reopen","item":"i9","anchor":{"ref":"review:1"}}]}`
	got := agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"abc123abc123","record":`+rec+`}`)
	var ended api.TurnEnded
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &ended) != nil || ended.Ledger == nil {
		t.Fatalf("end: %d %s", got.Code, got.Body)
	}
	if len(ended.Ledger.Applied) != 1 || len(ended.Ledger.Proposed) != 1 || len(ended.Ledger.Refused) != 1 || ended.Ledger.Refused[0].Index != 2 ||
		ended.Ledger.Applied[0].Op.Unit != agentRef.Unit() {
		t.Fatalf("ledger %+v", ended.Ledger)
	}
	if len(l.Calls) != 1 {
		t.Fatalf("calls %d", len(l.Calls))
	}

	plain := agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"abc123abc123","record":`+doneReview+`}`)
	var again api.TurnEnded
	if json.Unmarshal(plain.Body.Bytes(), &again) != nil || again.Ledger != nil || again.Note != "" {
		t.Fatalf("a record without ops: %s", plain.Body)
	}
	late := agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"abc123abc123","record":`+strings.Replace(rec, `"reviewed"`, `"again"`, 1)+`}`)
	var dropped api.TurnEnded
	if json.Unmarshal(late.Body.Bytes(), &dropped) != nil || dropped.Ledger != nil || !strings.Contains(dropped.Note, "Ledger: not applied; this turn already ended") {
		t.Fatalf("dropped ops: %s", late.Body)
	}
}
