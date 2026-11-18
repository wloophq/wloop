package cli

import (
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
)

// serveProjects runs the project verbs and the Review on g.
func serveProjects(g *coretasks.Service, db *sqlite.DB) {
	if g == nil || db == nil {
		return
	}
	g.ServeProjects(coretasks.ProjectDeps{Store: db.Projects()})
}
