package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

type projectStore struct{ db *DB }

const (
	currentRunSQL = `SELECT id, started_at, done FROM review_runs WHERE finished_at = '' ORDER BY started_at DESC, id DESC LIMIT 1`
)

var _ coretasks.ProjectStore = projectStore{}

// Projects are the Review's runs; a project's own fields are read and written with its task.
func (db *DB) Projects() coretasks.ProjectStore { return projectStore{db} }

func (s projectStore) CurrentRun(ctx context.Context) (task.ReviewRun, bool, error) {
	var r task.ReviewRun
	var started, done string
	err := s.db.R().QueryRowContext(ctx, currentRunSQL).Scan(&r.ID, &started, &done)
	if errors.Is(err, sql.ErrNoRows) {
		return task.ReviewRun{}, false, nil
	}
	if err != nil {
		return task.ReviewRun{}, false, err
	}
	if r.StartedAt, err = taskTime(started); err != nil {
		return task.ReviewRun{}, false, fmt.Errorf("review run %s: %w", r.ID, err)
	}
	if done != "" {
		if err := json.Unmarshal([]byte(done), &r.Done); err != nil {
			return task.ReviewRun{}, false, fmt.Errorf("review run %s: %w", r.ID, err)
		}
	}
	return r, true, nil
}

func (s projectStore) PutRun(ctx context.Context, r task.ReviewRun) error {
	if r.ID == "" || r.StartedAt.IsZero() {
		return errors.New("a review run needs an id and a start")
	}
	done := ""
	if len(r.Done) > 0 {
		b, err := json.Marshal(r.Done)
		if err != nil {
			return err
		}
		done = string(b)
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO review_runs (id, started_at, finished_at, done) VALUES (?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET started_at = excluded.started_at, finished_at = excluded.finished_at, done = excluded.done`,
			r.ID, eventTime(r.StartedAt), eventTime(r.FinishedAt), done)
		return err
	})
	return err
}
