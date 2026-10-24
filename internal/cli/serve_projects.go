package cli

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/dossiers"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/dossier"
)

// serveProjects runs the project verbs and the weekly Review on g, the projects' dossiers kept by ds.
func serveProjects(g *coregtd.Service, db *sqlite.DB, ds *dossiers.Service) {
	if g == nil || db == nil {
		return
	}
	d := coregtd.ProjectDeps{Store: db.Projects()}
	if ds != nil {
		d.Dossiers = projectDossiers{ds}
	}
	g.ServeProjects(d)
}

type projectDossiers struct{ s *dossiers.Service }

func (p projectDossiers) Open(ctx context.Context, slug string) error {
	_, err := p.s.Open(ctx, dossier.Owner{Project: slug})
	return err
}

func (p projectDossiers) Resolved(ctx context.Context, slug string, at time.Time) error {
	return p.s.Resolved(ctx, dossier.Owner{Project: slug}, at)
}
