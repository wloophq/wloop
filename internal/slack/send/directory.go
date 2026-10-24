package send

import (
	"context"
	"net/url"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/slack"
)

// directoryTTL bounds how long a listing of users or conversations answers name lookups.
// users.list is Tier 2, so a large workspace cannot be paged on every send; a name the
// cached listing misses is looked up again with a fresh one.
const directoryTTL = 10 * time.Minute

type directory struct {
	mu       sync.Mutex
	now      func() time.Time
	userList listing[slack.UserInfo]
	convList listing[slack.ConversationInfo]
}

type listing[T any] struct {
	items []T
	at    time.Time
}

func (d *directory) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// users returns every user of the workspace and whether the list came from the cache.
func (d *directory) users(ctx context.Context, c *slack.Client, refresh bool) ([]slack.UserInfo, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return cached(d, &d.userList, refresh, func() ([]slack.UserInfo, error) {
		var all []slack.UserInfo
		cursor := ""
		for {
			page, err := c.ListUsers(ctx, cursor)
			if err != nil {
				return nil, err
			}
			all = append(all, page.Users...)
			if cursor = page.NextCursor; cursor == "" {
				return all, nil
			}
		}
	})
}

// conversations returns the channels and group DMs the user is a member of, archived ones excluded.
func (d *directory) conversations(ctx context.Context, c *slack.Client, refresh bool) ([]slack.ConversationInfo, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return cached(d, &d.convList, refresh, func() ([]slack.ConversationInfo, error) {
		var all []slack.ConversationInfo
		args := url.Values{"types": {"public_channel,private_channel,mpim"}, "exclude_archived": {"true"}, "limit": {"200"}}
		for {
			var out struct {
				Channels         []slack.ConversationInfo `json:"channels"`
				ResponseMetadata struct {
					NextCursor string `json:"next_cursor"`
				} `json:"response_metadata"`
			}
			if err := c.Call(ctx, "users.conversations", args, &out); err != nil {
				return nil, err
			}
			all = append(all, out.Channels...)
			if out.ResponseMetadata.NextCursor == "" {
				return all, nil
			}
			args.Set("cursor", out.ResponseMetadata.NextCursor)
		}
	})
}

func cached[T any](d *directory, l *listing[T], refresh bool, fetch func() ([]T, error)) ([]T, bool, error) {
	now := d.clock()
	if !refresh && l.items != nil && now.Sub(l.at) < directoryTTL {
		return l.items, true, nil
	}
	items, err := fetch()
	if err != nil {
		return nil, false, err
	}
	if items == nil {
		items = []T{}
	}
	l.items, l.at = items, now
	return items, false, nil
}
