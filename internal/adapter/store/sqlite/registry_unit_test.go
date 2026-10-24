package sqlite

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestSessionKeepsItsUnit(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := unit.Unit{Provider: "slack", Key: "T000/C001/1.1"}
	if err := s.SaveSession(ctx, turn.Session{ID: "term_1", WorkspaceID: "ws", Unit: u}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Session(ctx, "term_1")
	if err != nil || !ok || got.Unit != u {
		t.Fatalf("Session = %+v %v %v", got, ok, err)
	}
	all, err := s.Sessions(ctx)
	if err != nil || len(all) != 1 || all[0].Unit != u {
		t.Fatalf("Sessions = %+v %v", all, err)
	}
}
