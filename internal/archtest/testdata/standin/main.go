// Command standin is a source plugin that lives in its own directory and
// imports only the SDK: its manifest declares a fact, a temporal fact, a
// delivery, a signal, a task kind and a tool, its profile layer words the
// tool, and it keeps its notes in its own store.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/store"
)

func main() {
	sdk.Serve(map[sdk.Kind]sdk.Impl{src.Kind: src.New(&standin{})})
}

// shelvedAt is when the stand-in's one note went on its shelf.
var shelvedAt = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

type standin struct {
	mu   sync.Mutex
	db   *sql.DB
	host src.Host
}

var migrations = []store.Migration{{Version: 1, Up: func(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE notes (text TEXT NOT NULL)`)
	return err
}}}

func (s *standin) Health(context.Context) (sdk.Health, error) {
	return sdk.Health{Status: sdk.StatusServing}, nil
}

func (s *standin) Start(ctx context.Context, req src.StartRequest) error {
	db, err := store.Open(ctx, req.StateDir, store.Options{Migrations: migrations})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.db, s.host = db, req.Host
	s.mu.Unlock()
	note := sdk.Unit{Provider: "standin", Key: "attic/1"}
	return req.Host.Report(ctx, []src.Snapshot{{
		Unit: note, Rev: 1, At: shelvedAt,
		Data:    json.RawMessage(`{"title":"Water the plants"}`),
		Card:    &src.Card{Title: "Water the plants"},
		Facts:   []src.Fact{{Name: "standin.shelf", Value: src.String("attic")}, {Name: "standin.shelved_for", Value: src.Since(shelvedAt)}},
		Signals: []src.Signal{{Name: "shelved", On: true, Since: shelvedAt}},
	}})
}

func (s *standin) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *standin) Notify(context.Context, []sdk.Unit, []sdk.Topic) error { return nil }

func (s *standin) CallTool(ctx context.Context, call src.ToolCall) (src.ToolResult, error) {
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return src.ToolResult{Answer: "bad_arguments", Fields: fields(map[string]any{"Error": err.Error()}), IsError: true}, nil
	}
	if strings.TrimSpace(args.Text) == "" {
		return src.ToolResult{Answer: "problem_text_required", IsError: true}, nil
	}
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if _, err := db.ExecContext(ctx, `INSERT INTO notes (text) VALUES (?)`, args.Text); err != nil {
		return src.ToolResult{}, err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes`).Scan(&n); err != nil {
		return src.ToolResult{}, err
	}
	return src.ToolResult{Answer: "noted", Fields: fields(map[string]any{"Text": args.Text, "Count": n})}, nil
}

func fields(m map[string]any) json.RawMessage {
	raw, _ := json.Marshal(m)
	return raw
}
