package source_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// waker is a source whose ingestion wakes its extraction and says when it next has work.
type waker struct{ *sourcetest.Stub }

func (s waker) Ingest(ctx context.Context, _ source.IngestRequest) (source.IngestResult, error) {
	if err := s.Host().WakeJob(ctx, "extract", t0.Add(90*time.Second)); err != nil {
		return source.IngestResult{}, err
	}
	return source.IngestResult{Summary: "read", Next: t0.Add(time.Hour)}, nil
}

type jobWakes struct {
	mu    sync.Mutex
	woken []string
}

func (j *jobWakes) WakeJob(_ context.Context, job string, at time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.woken = append(j.woken, job+"@"+at.UTC().Format(time.RFC3339))
	return nil
}

func TestAJobsNextAndAWakeCrossTheWire(t *testing.T) {
	jobs := &jobWakes{}
	c := sourcetest.Launch(t, "pulls", waker{sourcetest.NewStub()}, source.Host{Jobs: jobs})
	res, err := c.Ingest(t.Context(), source.IngestRequest{Job: "ingest", Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Next.Equal(t0.Add(time.Hour)) {
		t.Errorf("next %v, want %v", res.Next, t0.Add(time.Hour))
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if want := "extract@" + t0.Add(90*time.Second).UTC().Format(time.RFC3339); len(jobs.woken) != 1 || jobs.woken[0] != want {
		t.Errorf("woken %v, want %s", jobs.woken, want)
	}
}
