package taskstest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Props is an in-memory coretasks.PropStore over the tasks Add names.
// Writers holds the writer of each property, by task and name.
type Props struct {
	mu      sync.Mutex
	props   map[task.ID]task.Props
	Writers map[task.ID]map[string]task.Writer
}

var _ coretasks.PropStore = (*Props)(nil)

func NewProps(ids ...task.ID) *Props {
	p := &Props{props: map[task.ID]task.Props{}, Writers: map[task.ID]map[string]task.Writer{}}
	for _, id := range ids {
		p.Add(id)
	}
	return p
}

// Add makes id a task SetProp accepts.
func (p *Props) Add(id task.ID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.props[id] == nil {
		p.props[id] = task.Props{}
		p.Writers[id] = map[string]task.Writer{}
	}
}

func (p *Props) SetProp(_ context.Context, id task.ID, name string, v task.Value, w task.Writer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.props[id] == nil {
		return fmt.Errorf("%w: %s", coretasks.ErrNoTask, id)
	}
	p.props[id][name] = v
	p.Writers[id][name] = w
	return nil
}

func (p *Props) PropsOf(_ context.Context, id task.ID) (task.Props, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.props[id]), nil
}

// PropContract is the suite every coretasks.PropStore passes: open returns a
// store and a func that creates a task in it.
func PropContract(t *testing.T, open func(t *testing.T) (coretasks.PropStore, func(task.ID))) {
	ctx := context.Background()
	t.Run("a set value reads back in its type", func(t *testing.T) {
		s, add := open(t)
		add("t-01")
		want := task.Props{
			"priority": {Text: "high"},
			"size":     {Dur: 30 * time.Minute},
			"due":      {Time: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)},
			"count":    {Int: 3},
			"contexts": {List: []string{"@desk", "@call"}},
			"asker":    {Actors: []items.Person{{Key: "slack:U01", Name: "Ana"}}},
		}
		for _, name := range slices.Sorted(maps.Keys(want)) {
			if err := s.SetProp(ctx, "t-01", name, want[name], task.WriterPerson); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.PropsOf(ctx, "t-01")
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("props %v, want %v", got, want)
		}
	})
	t.Run("a second set replaces the value", func(t *testing.T) {
		s, add := open(t)
		add("t-01")
		for _, v := range []string{"low", "high"} {
			if err := s.SetProp(ctx, "t-01", "priority", task.Value{Text: v}, task.WriterAgent); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.PropsOf(ctx, "t-01")
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := got.Get("priority"); !ok || v.Text != "high" || len(got) != 1 {
			t.Fatalf("props %v", got)
		}
	})
	t.Run("a task with none has empty props", func(t *testing.T) {
		s, add := open(t)
		add("t-01")
		got, err := s.PropsOf(ctx, "t-01")
		if err != nil || len(got) != 0 {
			t.Fatalf("props %v, %v", got, err)
		}
	})
	t.Run("an unknown task is refused", func(t *testing.T) {
		s, _ := open(t)
		if err := s.SetProp(ctx, "t-404", "priority", task.Value{Text: "high"}, task.WriterPerson); !errors.Is(err, coretasks.ErrNoTask) {
			t.Fatalf("err %v", err)
		}
	})
}
