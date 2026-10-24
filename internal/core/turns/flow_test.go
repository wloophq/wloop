package turns_test

import (
	"context"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/echo"
	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/view"
)

func TestFlowRunsWithoutEditingTheCore(t *testing.T) {
	s, store, _, sess, _ := newService(t)
	ctx := context.Background()
	board := testsink.New("test")
	v := &views.Service{Books: store, Intents: turnstest.Intents(), State: viewstest.NewState(viewstest.Strict(t)), Sinks: []views.Sink{{ViewSink: board, Placements: board}}}
	s.Changed = v.Changed
	vctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { v.Run(vctx); close(done) }()
	defer func() { stop(); <-done }()
	column := func(want view.Status) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for c, _, _ := board.Card(r7); c != board.Column(want); c, _, _ = board.Card(r7) {
			if time.Now().After(deadline) {
				t.Fatalf("the card is in %q, want %q", c, board.Column(want))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	agent := &echo.Agent{
		Start: func(ctx context.Context, handle, round string) error {
			_, err := s.Start(ctx, handle, round)
			return err
		},
		Record: func(ctx context.Context, handle, round string, r record.Record) error {
			_, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: handle, Turn: round, Via: "mcp", Record: r})
			return err
		},
		Reply: func(_, line string) (record.Record, bool) {
			if strings.Contains(line, "--turn") {
				return askedRecord(), true
			}
			return record.Record{State: record.Done, Summary: "approved",
				Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://github.com/acme/api/pull/7#pullrequestreview-1"}}, true
		},
	}
	sess.OnType = agent.Type

	s.NewID = func() string { return "term_1" }
	if _, err := s.Resolve(ctx, "orca", "term_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MCPSeen(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	sess.Put("ws", turns.Terminal{Session: "term_1", LastOutput: t0})

	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Head: "h1", Records: true}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Capable(ctx, "term_1"); err != nil || !ok {
		t.Fatalf("capable %v %v", ok, err)
	}
	if err := s.SentTo(ctx, r7, "abc", "term_1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := sess.Type(ctx, "term_1", "/"+brand.Name+"-review "+"https://github.com/acme/api/pull/7"+" --turn abc"); err != nil || !ok {
		t.Fatalf("typed the turn: %v %v", ok, err)
	}
	b := bookOf(t, store, r7)
	if b.Turns[0].State != turn.Asked || b.Turns[0].Session != "term_1" {
		t.Fatalf("after the first line: %+v", b.Turns[0])
	}
	column(view.NeedsMe)

	got, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, OptionID: "publish", Source: turn.Notification})
	if err != nil || got.Route != turn.Typed {
		t.Fatalf("answer %+v %v", got, err)
	}
	b = bookOf(t, store, r7)
	h := b.History(10)
	if b.Turns[0].State != turn.Done || len(h) != 1 || h[0].Verdict != "APPROVE" || h[0].Endorsed != turn.EndorsedApproved {
		t.Fatalf("turn %+v, history %+v", b.Turns[0], h)
	}
	column(view.Done)

	fset := token.NewFileSet()
	err = filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); strings.HasSuffix(p, "/agent/echo") {
				t.Errorf("%s imports %s", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
