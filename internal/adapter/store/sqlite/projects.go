package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

type projectStore struct{ db *DB }

const (
	slugTakenSQL  = `SELECT EXISTS (SELECT 1 FROM projects WHERE slug = ?1 AND slug != '')`
	currentRunSQL = `SELECT id, started_at, done FROM review_runs WHERE finished_at = '' ORDER BY started_at DESC, id DESC LIMIT 1`
)

var _ coretasks.ProjectStore = projectStore{}

// Projects are the projects' slugs and the Review's runs; a project's own fields are read and written with its
// entry.
func (db *DB) Projects() coretasks.ProjectStore { return projectStore{db} }

// loadProjects sets the project fields of the entries of out that the projects table holds, sub selecting
// their ids with args.
func loadProjects(ctx context.Context, q querier, sub string, args []any, out []task.Entry, at map[task.ID]int) error {
	return eachRow(ctx, q, `SELECT entry_id, slug, review_every, reviewed_at FROM projects WHERE entry_id IN `+sub, args,
		func(r *sql.Rows) error {
			var id, slug, reviewed string
			var every int64
			if err := r.Scan(&id, &slug, &every, &reviewed); err != nil {
				return err
			}
			e := &out[at[task.ID(id)]]
			t, err := entryTime(reviewed)
			if err != nil {
				return fmt.Errorf("the review time of project %s: %w", id, err)
			}
			e.Slug, e.ReviewEvery, e.ReviewedAt = slug, time.Duration(every)*time.Second, t
			return nil
		})
}

// writeProject keeps a project's row; other kinds have none.
func writeProject(ctx context.Context, tx *Tx, e task.Entry) error {
	if e.Kind != task.KindProject {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO projects (entry_id, slug, review_every, reviewed_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (entry_id) DO UPDATE SET slug = excluded.slug, review_every = excluded.review_every, reviewed_at = excluded.reviewed_at`,
		string(e.ID), e.Slug, int64(e.ReviewEvery/time.Second), eventTime(e.ReviewedAt))
	return err
}

func (s projectStore) SlugTaken(ctx context.Context, slug string) (bool, error) {
	var taken bool
	err := s.db.R().QueryRowContext(ctx, slugTakenSQL, slug).Scan(&taken)
	return taken, err
}

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
	if r.StartedAt, err = entryTime(started); err != nil {
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
