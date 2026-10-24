package ghsource

import (
	"context"
	"errors"

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
