package tasks

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/task"
)

// Match is every open task whose title holds text, case and the spaces around text aside, the newest change
// first: the tasks a person picks another task from. Draft and message tasks are left out, as no verb takes
// them as the other task.
func (s *Service) Match(ctx context.Context, text string) ([]task.Task, error) {
	open, err := s.d.Store.Tasks(ctx, task.Query{})
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSpace(text))
	out := slices.DeleteFunc(open, func(e task.Task) bool {
		return e.Origin == task.OriginDraft || e.Origin == task.OriginMessage || !strings.Contains(strings.ToLower(e.Title), want)
	})
	slices.SortStableFunc(out, func(a, b task.Task) int { return cmp.Or(b.Updated.Compare(a.Updated), cmp.Compare(a.ID, b.ID)) })
	return out, nil
}
