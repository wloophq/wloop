package tasks

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/task"
)

// Match is every open task whose title holds text, case and the spaces around text aside, the newest change
// first: the tasks a person picks another task from for task except, which is left out as it reads. Draft and
// message tasks are left out too, as no verb takes them as the other task.
func (s *Service) Match(ctx context.Context, text string, except task.ID) ([]task.Task, error) {
	if except != "" {
		e, err := s.d.Store.Task(ctx, except)
		switch {
		case err == nil:
			except = e.ID
		case !errors.Is(err, ErrNoTask):
			return nil, err
		}
	}
	open, err := s.d.Store.Tasks(ctx, task.Query{})
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSpace(text))
	out := slices.DeleteFunc(open, func(e task.Task) bool {
		return e.ID == except || e.Origin == task.OriginDraft || e.Origin == task.OriginMessage || !strings.Contains(strings.ToLower(e.Title), want)
	})
	slices.SortStableFunc(out, func(a, b task.Task) int { return cmp.Or(b.Updated.Compare(a.Updated), cmp.Compare(a.ID, b.ID)) })
	return out, nil
}
