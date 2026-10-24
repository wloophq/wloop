package slack

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	slackapi "github.com/wloophq/wloop/internal/slack"
)

// UserTTL is how long a user read from Slack is served from slack_users.
const UserTTL = 24 * time.Hour

const slackbot = "USLACKBOT"

// userCache resolves a user id from memory, then slack_users, then one
// users.info call shared by every concurrent caller.
type userCache struct {
	db     *sqlite.SlackStore
	client *slackapi.Client
	now    func() time.Time

	mu     sync.Mutex
	mem    map[string]dslack.User
	flight map[string]*userFlight
}

type userFlight struct {
	done chan struct{}
	u    dslack.User
	err  error
}

func (c *userCache) fresh(u dslack.User) bool { return c.now().Sub(u.Fetched) < UserTTL }

func (c *userCache) get(ctx context.Context, id string) (dslack.User, error) {
	if id == slackbot {
		return dslack.User{ID: id, IsBot: true}, nil
	}
	c.mu.Lock()
	if u, ok := c.mem[id]; ok && c.fresh(u) {
		c.mu.Unlock()
		return u, nil
	}
	if f, ok := c.flight[id]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.u, f.err
		case <-ctx.Done():
			return dslack.User{}, ctx.Err()
		}
	}
	if c.flight == nil {
		c.flight, c.mem = map[string]*userFlight{}, map[string]dslack.User{}
	}
	f := &userFlight{done: make(chan struct{})}
	c.flight[id] = f
	c.mu.Unlock()

	f.u, f.err = c.fetch(ctx, id)
	c.mu.Lock()
	delete(c.flight, id)
	if f.err == nil {
		c.mem[id] = f.u
	}
	c.mu.Unlock()
	close(f.done)
	return f.u, f.err
}

func (c *userCache) fetch(ctx context.Context, id string) (dslack.User, error) {
	rows, err := c.db.Users(ctx, []string{id})
	if err != nil {
		return dslack.User{}, err
	}
	if len(rows) == 1 {
		if u := user(rows[0]); c.fresh(u) {
			return u, nil
		}
	}
	info, err := c.client.UserInfo(ctx, id)
	if err != nil {
		return dslack.User{}, err
	}
	at := c.now().UTC().Truncate(time.Second)
	row := sqlite.UserRow{ID: id, Name: info.Name, DisplayName: info.DisplayName, Email: info.Email, IsBot: info.IsBot,
		Deleted: info.Deleted, FetchedAt: at}
	if err := c.db.PutUsers(ctx, []sqlite.UserRow{row}); err != nil {
		return dslack.User{}, err
	}
	return user(row), nil
}
