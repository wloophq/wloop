package ghsource

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type viewerStore interface {
	Viewer(ctx context.Context, provider unit.ProviderID) (string, error)
}

// identity is the user as GitHub knows them: the stored viewer login.
func identity(st viewerStore) func(context.Context) (items.Person, error) {
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

type viewerReader interface {
	Viewer(ctx context.Context) (string, error)
}

// Identity is the user as GitHub knows them, the Identity the plugin's
// Presenter serves: the stored viewer login, which it reads from gh and
// stores when none is, and the teams the scope resolved.
func Identity(st *inventory.Store, gh viewerReader, logf func(format string, args ...any)) func(context.Context) (src.Identity, error) {
	return func(ctx context.Context) (src.Identity, error) {
		login, err := st.Viewer(ctx, unit.GitHub)
		if err != nil {
			return src.Identity{}, err
		}
		if login == "" {
			if login, err = gh.Viewer(ctx); err != nil {
				return src.Identity{}, err
			}
			if err := st.SetViewer(ctx, unit.GitHub, login); err != nil {
				logf("viewer: %v", err)
			}
		}
		teams, err := MyTeams(ctx, st)
		p := person(login)
		return src.Identity{Viewer: src.Person{Key: p.Key, Name: p.Name}, Teams: teams}, err
	}
}
