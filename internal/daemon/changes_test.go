package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func withLog(t *testing.T, s *Server) *change.Log {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	s.deps.Changes = log
	return log
}

func noted(t *testing.T, log *change.Log, from port.Generation) port.Change {
	t.Helper()
	return log.Since(from, ^port.Topic(0))
}

func TestJobEndNotesJobsOnly(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	log := withLog(t, s)
	j := newJob("t", time.Hour, time.Minute, func(context.Context) (string, string, error) { return "ok", "", nil })
	j.onEnd = s.jobEnded
	j.once(context.Background(), func(string, ...any) {}, s.jobDone)
	c := noted(t, log, 0)
	if c.Topics != port.TopicJobs || c.All || len(c.Units) != 0 || log.Gen() != 1 {
		t.Fatalf("%+v gen %d", c, log.Gen())
	}
}

func TestFailedJobRunNotesJobsWithoutHoldingItsLock(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	log := withLog(t, s)
	j := newJob("t", time.Hour, time.Minute, func(context.Context) (string, string, error) { return "", "", errors.New("boom") })
	j.onEnd = s.jobEnded
	var seen string
	log.Subscribe(func(port.Change) { seen = j.status().LastErr })
	s.warnings.set("t", "stale")
	j.once(context.Background(), func(string, ...any) {}, s.jobDone)
	if c := noted(t, log, 0); c.Topics != port.TopicJobs || log.Gen() != 1 {
		t.Fatalf("%+v gen %d", c, log.Gen())
	}
	if seen != "boom" {
		t.Fatalf("subscriber read LastErr %q", seen)
	}
	if len(s.warnings.list()) != 1 {
		t.Fatal("a failed run changed the warning")
	}
}

func TestForceableNotesRefusals(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	log := withLog(t, s)
	refusal := Refusal{Reason: "dirty", Forceable: true}
	s.rememberForceable(r7.Unit(), refusal)
	s.rememberForceable(r7.Unit(), refusal)
	if log.Gen() != 1 {
		t.Fatalf("an unchanged refusal noted again: gen %d", log.Gen())
	}
	c := noted(t, log, 0)
	if c.Topics != port.TopicRefusals || !slices.Equal(c.Units, []unit.Unit{r7.Unit()}) {
		t.Fatalf("%+v", c)
	}
	s.rememberForceable(r7.Unit(), nil)
	s.rememberForceable(r7.Unit(), nil)
	if log.Gen() != 2 {
		t.Fatalf("gen %d, want 2", log.Gen())
	}
}

func TestAppStatusNotesApp(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	log := withLog(t, s)
	if rec := post(t, s, "/api/app", `{"state":"running","notifications":"denied"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if c := noted(t, log, 0); c.Topics != port.TopicApp || len(c.Units) != 0 {
		t.Fatalf("%+v", c)
	}
	post(t, s, "/api/app", `{"state":"running","notifications":"denied"}`)
	if log.Gen() != 1 {
		t.Fatalf("an identical status noted again: gen %d", log.Gen())
	}
}
