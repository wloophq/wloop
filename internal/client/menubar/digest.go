package menubar

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// digests posts the daily digest once a day at DigestAt, or at start when
// that time has passed and today's was not posted, until ctx ends.
func (c *Controller) digests(ctx context.Context) {
	at, err := time.Parse("15:04", c.cfg.DigestAt)
	if err != nil {
		c.cfg.Log("digest: notifications.digest_at %q is not HH:MM", c.cfg.DigestAt)
		return
	}
	for {
		now := c.cfg.Now().Local()
		due := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, time.Local)
		wait := due.Sub(now)
		if wait <= 0 {
			if err := c.postDigest(ctx, now.Format(time.DateOnly)); err != nil {
				if !errors.Is(err, errNotShown) {
					c.cfg.Log("digest: %v", err)
				}
				wait = refreshMax
			} else {
				wait = due.AddDate(0, 0, 1).Sub(now)
			}
		}
		if !c.sleep(ctx, wait) {
			return
		}
	}
}

var errNotShown = errors.New("notifications are off")

// postDigest posts day's digest unless it was posted, or found empty,
// already; an error leaves it to be tried again.
func (c *Controller) postDigest(ctx context.Context, day string) error {
	id := api.NotificationID(api.CategoryDigest, "-", day)
	c.mu.Lock()
	done := c.daily[id]
	c.mu.Unlock()
	if done {
		return nil
	}
	if !c.mayPost() {
		return errNotShown
	}
	d, err := c.cfg.Daemon.Digest(ctx, day)
	if err != nil {
		return err
	}
	if len(d.Tasks) > 0 {
		if err := c.cfg.Notifier.Post(Notification{ID: id, Title: d.Title, Body: d.Body, Category: api.CategoryDigest}); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.daily[id] = true
	c.mu.Unlock()
	c.saveSent(day)
	return nil
}
