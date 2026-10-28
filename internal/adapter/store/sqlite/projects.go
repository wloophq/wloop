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
	slugTakenSQL  = `SELECT EXISTS (SELECT 1 FROM projects WHERE slug = ?1 AND slug != '')`
	currentRunSQL = `SELECT id, started_at, done FROM review_runs WHERE finished_at = '' ORDER BY started_at DESC, id DESC LIMIT 1`
)

var _ coretasks.ProjectStore = projectStore{}

// Projects are the projects' slugs and the Review's runs; a project's own fields are read and written with its
// task.
func (db *DB) Projects() coretasks.ProjectStore { return projectStore{db} }

// loadProjects sets the slugs of the tasks of out that the projects table holds, sub selecting their ids with
// args.
func loadProjects(ctx context.Context, q querier, sub string, args []any, out []task.Task, at map[task.ID]int) error {
	return eachRow(ctx, q, `SELECT task_id, slug FROM projects WHERE task_id IN `+sub, args,
		func(r *sql.Rows) error {
			var id, slug string
			if err := r.Scan(&id, &slug); err != nil {
				return err
			}
			out[at[task.ID(id)]].Slug = slug
			return nil
		})
}

// writeProject keeps a project's row; other kinds have none.
func writeProject(ctx context.Context, tx *Tx, e task.Task) error {
	if e.Kind != task.KindProject {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO projects (task_id, slug) VALUES (?, ?)
		ON CONFLICT (task_id) DO UPDATE SET slug = excluded.slug`, string(e.ID), e.Slug)
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
