package cli

import (
	"cmp"
	"context"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/dossiers"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/meter"
)

// dossierSweepEvery is how often the daemon runs the dossiers' lifecycle job.
const dossierSweepEvery = 6 * time.Hour

func dossierPolicy(c config.Dossiers) dossier.Policy {
	d := dossier.DefaultPolicy
	pos := func(n int64) int64 { return max(n, 0) }
	return dossier.Policy{
		ArchiveAfter: cmp.Or(time.Duration(max(c.ArchiveAfter, 0)), d.ArchiveAfter),
		PurgeAfter:   cmp.Or(time.Duration(max(c.PurgeAfter, 0)), d.PurgeAfter),
		SoftCap:      cmp.Or(pos(c.SoftCapMB)*dossier.MB, d.SoftCap),
		TotalCap:     cmp.Or(pos(c.TotalCapMB)*dossier.MB, d.TotalCap),
		MaxFile:      cmp.Or(pos(c.MaxFileMB)*dossier.MB, d.MaxFile),
		CompactAt:    cmp.Or(pos(c.CompactAtKB)*dossier.KB, d.CompactAt),
	}
}

// serveDossiers builds the dossiers service on db and, unless --no-jobs, sweeps it until ctx ends;
// nil without a store.
func (e *Env) serveDossiers(ctx context.Context, db *sqlite.DB, cfg config.Config, logf func(string, ...any)) *dossiers.Service {
	if db == nil {
		return nil
	}
	p := dossierPolicy(cfg.Dossiers)
	s := &dossiers.Service{Store: db.Dossiers(), Files: e.dossierFiles(p), Policy: p, StateDir: e.StateDir, Clock: clockFunc(e.Now)}
	if e.noJobs {
		return s
	}
	go func() {
		t := time.NewTicker(dossierSweepEvery)
		defer t.Stop()
		for {
			lctx, scope := meter.Start(ctx, meter.Loop("dossiers"))
			archived, purged, err := s.Sweep(lctx)
			scope.End()
			if err != nil && ctx.Err() == nil {
				logf("dossiers: %v", err)
			}
			if archived+purged > 0 {
				logf("dossiers: archived %d, purged %d", archived, purged)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return s
}
