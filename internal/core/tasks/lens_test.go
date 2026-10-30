package tasks_test

import (
	"context"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// testView is a view of testLens: a Go predicate standing for the profile's.
type testView struct {
	spec coretasks.ViewSpec
	in   func(task.Task) bool
}

// testLens are views over the categories and properties the service's tests set, standing for a profile's.
type testLens struct {
	views  []testView
	badge  []task.View
	review []task.View
}

func newTestLens() *testLens {
	cat := func(c task.Category) func(task.Task) bool { return func(e task.Task) bool { return e.Category == c } }
	return &testLens{
		views: []testView{
			{coretasks.ViewSpec{ID: "inbox", Over: task.OverTasks, Paged: true, HideDisabled: true}, cat(task.CategoryInbox)},
			{coretasks.ViewSpec{ID: "ready", Over: task.OverTasks, Paged: true, HideDisabled: true}, cat(task.CategoryReady)},
			{coretasks.ViewSpec{ID: "waiting", Over: task.OverTasks, Paged: true}, cat(task.CategoryWaiting)},
			{coretasks.ViewSpec{ID: "starred", Over: task.OverTasks, Paged: true, HideDisabled: true},
				func(e task.Task) bool { return e.Prop("focus").Text == "on" && e.Open() }},
			{coretasks.ViewSpec{ID: "logbook", Over: task.OverTasks, Paged: true}, cat(task.CategoryDone)},
			{coretasks.ViewSpec{ID: "drafts", Over: task.OverGates}, nil},
		},
		badge:  []task.View{"inbox", "drafts"},
		review: []task.View{"inbox", "drafts", "waiting"},
	}
}

func (l *testLens) add(v testView) { l.views = append(l.views, v) }

func (l *testLens) Views() coretasks.Views {
	out := coretasks.Views{Badge: l.badge, Review: l.review}
	for _, v := range l.views {
		out.List = append(out.List, v.spec)
	}
	return out
}

func (l *testLens) Eval(_ context.Context, view string, t task.Task) (bool, error) {
	for _, v := range l.views {
		if string(v.spec.ID) == view && v.in != nil {
			return v.in(t), nil
		}
	}
	return false, nil
}
