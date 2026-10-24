package slack

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/domain/items"
)

// identity is the user as Slack knows them: the logged-in viewer, named and
// aliased by email from the user cache.
func (p *plugin) identity(ctx context.Context) (items.Person, error) {
	if p.src == nil {
		return items.Person{}, errors.New("slack: no store")
	}
	_, user, err := p.src.store.Viewer(ctx)
	if err != nil {
		return items.Person{}, err
	}
	who := items.Person{Key: "slack:" + user}
	users, err := p.src.store.Users(ctx, []string{user})
	if err != nil {
		return items.Person{}, err
	}
	if u, ok := users[user]; ok {
		who.Name = userName(user, users)
		if u.Email != "" {
			who.Aliases = []string{"email:" + u.Email}
		}
	}
	return who, nil
}
