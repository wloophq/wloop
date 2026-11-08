package source_test

import (
	"context"
	"errors"
	"testing"
	"time"

	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestWakeJobRunsThePluginsOwnJobByItsHostName(t *testing.T) {
	t.Parallel()
	type wake struct {
		name string
		at   time.Time
	}
	var got []wake
	b := &psource.Broker{ID: "chat", Manifest: &manifest.Source{Jobs: []manifest.Job{{Name: "ingest"}, {Name: "extract"}}},
		WakeJob: func(name string, at time.Time) bool { got = append(got, wake{name, at}); return true }}
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	h := b.Host()
	if err := h.WakeJob(context.Background(), "extract", at); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].name != "extract:chat" || !got[0].at.Equal(at) {
		t.Fatalf("woke %+v, want extract:chat at %v", got, at)
	}
	for _, job := range []string{"ingest", "other"} {
		if err := h.WakeJob(context.Background(), job, at); !errors.Is(err, src.ErrNotFound) {
			t.Errorf("a wake of %s: %v, want not found", job, err)
		}
	}
	if len(got) != 1 {
		t.Fatalf("a refused wake reached the host: %+v", got)
	}
	if err := (&psource.Broker{ID: "chat"}).Host().WakeJob(context.Background(), "extract", at); !errors.Is(err, src.ErrUnsupported) {
		t.Fatalf("a host without jobs answered %v, want unsupported", err)
	}
}
