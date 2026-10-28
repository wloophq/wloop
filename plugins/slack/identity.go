package slack

import (
	"context"
	"errors"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Identity is the user as Slack knows them: the logged-in viewer, named and
// aliased by email from the user cache.
func (p *plugin) Identity(ctx context.Context) (src.Identity, error) {
	switch {
	case !p.cfg.Configured():
		return src.Identity{}, errors.New(SkipNotConfigured)
	case p.src == nil:
		return src.Identity{}, errors.New("slack: no store")
	}
	_, user, err := p.src.store.Viewer(ctx)
	if err != nil {
		return src.Identity{}, err
	}
	who := src.Person{Key: "slack:" + user}
	users, err := p.src.store.Users(ctx, []string{user})
	if err != nil {
		return src.Identity{}, err
	}
	if u, ok := users[user]; ok {
		who.Name = userName(user, users)
		if u.Email != "" {
			who.Aliases = []string{"email:" + u.Email}
		}
	}
	return src.Identity{Viewer: who}, nil
}
