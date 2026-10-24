package catalog

import (
	"fmt"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

// The workspace roles a held round resolves its intent by, as a manifest's
// workspace names them.
const (
	RoleReview = "review"
	RoleAuthor = "author"
)

// ResolveRun is run.resolve: the drives_row intent whose workspace is the
// role of the held round's workspace; false when none or more than one is.
func ResolveRun(reg *dintent.Registry, role string) (dintent.Def, bool) {
	var found []dintent.Def
	for _, d := range reg.All() {
		if d.DrivesRow && d.Workspace == role {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		return dintent.Def{}, false
	}
	return found[0], true
}

// RoleFacts are what run.resolve reads of a pull request to pick the role
// of its held round.
type RoleFacts struct {
	ReviewWorkspace, AuthorWorkspace bool
	// Mine and Adopted tell a pull request the user opened or took over.
	Mine, Adopted bool
}

// WorkspaceRole is the role of the pull request's workspace: the linked
// one's, else the author's for a pull request that is the user's to develop.
func WorkspaceRole(f RoleFacts) string {
	switch {
	case f.ReviewWorkspace:
		return RoleReview
	case f.AuthorWorkspace, f.Mine, f.Adopted:
		return RoleAuthor
	}
	return RoleReview
}

// workspaceRole is WorkspaceRole of a pull request's workspace and
// override.
func workspaceRole(w api.Workspace, o api.Override, mine bool) string {
	linked := w.ID != ""
	return WorkspaceRole(RoleFacts{ReviewWorkspace: linked && w.IsReview, AuthorWorkspace: linked && !w.IsReview && !w.IsMain, Mine: mine, Adopted: o.Adopted})
}

// RunNowIntent is the intent a held round runs as on a pull request with
// workspace w and override o, or the error that says why no single one
// does.
func RunNowIntent(reg *dintent.Registry, w api.Workspace, o api.Override, mine bool) (string, error) {
	role := workspaceRole(w, o, mine)
	d, ok := ResolveRun(reg, role)
	if !ok {
		return "", fmt.Errorf("no single intent drives a row from a %s workspace", role)
	}
	return string(d.Name), nil
}
