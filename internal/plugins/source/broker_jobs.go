package source

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// JobName is the host's name of plugin id's manifest job job, one after its ingestion job.
func JobName(id, job string) string { return job + ":" + id }

type jobs struct{ b *Broker }

// WakeJob runs the plugin's manifest job named job at at. The ingestion job, the manifest's first, runs on its
// interval alone.
func (k jobs) WakeJob(_ context.Context, job string, at time.Time) error {
	if k.b.WakeJob == nil {
		return src.ErrUnsupported
	}
	if k.b.Manifest == nil || len(k.b.Manifest.Jobs) < 2 ||
		!slices.ContainsFunc(k.b.Manifest.Jobs[1:], func(j manifest.Job) bool { return j.Name == job }) {
		return fmt.Errorf("%w: %s declares no job %q after its ingestion", src.ErrNotFound, k.b.ID, job)
	}
	if !k.b.WakeJob(JobName(k.b.ID, job), at) {
		return fmt.Errorf("%w: the host runs no job %s", src.ErrNotFound, JobName(k.b.ID, job))
	}
	return nil
}
