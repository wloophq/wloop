package cli

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/dossiers"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/task"
)

// serveProjects runs the project verbs and the Review on g, the projects' dossiers kept by ds.
func serveProjects(g *coretasks.Service, db *sqlite.DB, ds *dossiers.Service) {
	if g == nil || db == nil {
		return
	}
	d := coretasks.ProjectDeps{Store: db.Projects()}
	if ds != nil {
		d.Dossiers = projectDossiers{ds}
	}
	g.ServeProjects(d)
}

// projectDossiers keeps a project's dossier, the one its task owns.
type projectDossiers struct{ s *dossiers.Service }

func (p projectDossiers) Open(ctx context.Context, id task.ID) error {
	_, err := p.s.Open(ctx, dossier.Owner{Unit: task.UnitOf(id)})
	return err
}

func (p projectDossiers) Resolved(ctx context.Context, id task.ID, at time.Time) error {
	return p.s.Resolved(ctx, dossier.Owner{Unit: task.UnitOf(id)}, at)
}
