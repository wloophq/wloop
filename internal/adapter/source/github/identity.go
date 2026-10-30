package ghsource

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/port"
	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type viewerStore interface {
	Viewer(ctx context.Context, provider unit.ProviderID) (string, error)
}

// IdentityOf is the user as GitHub knows them: the stored viewer login.
func IdentityOf(st viewerStore) func(context.Context) (items.Person, error) {
	return func(ctx context.Context) (items.Person, error) {
		login, err := st.Viewer(ctx, unit.GitHub)
		if err != nil {
			return items.Person{}, err
		}
		if login == "" {
			return items.Person{}, errors.New("github: no viewer stored yet")
		}
		return person(login), nil
	}
}

// metaTeams is the host's meta key of the viewer's teams, as the GitHub
// plugin last named them.
const metaTeams = "teams:github"

// KeepIdentity keeps id, the viewer as the GitHub plugin names them, in st:
// the login and the teams the in-process rows and facts read.
func KeepIdentity(ctx context.Context, st *inventory.Store, id src.Identity) error {
	if id.Viewer.Name != "" {
		if err := st.SetViewer(ctx, unit.GitHub, id.Viewer.Name); err != nil {
			return err
		}
	}
	teams := id.Teams
	if teams == nil {
		teams = []string{}
	}
	raw, err := json.Marshal(teams)
	if err != nil {
		return err
	}
	return st.SetMetaNoting(ctx, metaTeams, string(raw), port.TopicViewer)
}

// MyTeams are the scope teams the viewer belongs to, as the GitHub plugin
// last named them; nil before it named any or when they are unreadable.
func MyTeams(ctx context.Context, st *inventory.Store) ([]string, error) {
	raw, err := st.Meta(ctx, metaTeams)
	if err != nil || raw == "" {
		return nil, err
	}
	var teams []string
	if json.Unmarshal([]byte(raw), &teams) != nil {
		return nil, nil
	}
	return teams, nil
}
