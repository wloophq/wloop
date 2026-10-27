package slack

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
)

var (
	permalinkPath = regexp.MustCompile(`^/archives/([CGD][A-Z0-9]+)/p(\d{10})(\d{6})/?$`)
	permalinkTS   = regexp.MustCompile(`^\d{10}\.\d{6}$`)
)

// IsPermalink says whether s is a Slack message permalink.
func IsPermalink(s string) bool {
	_, _, _, err := parsePermalink(s)
	return err == nil
}

// FollowUnit is the thread unit a permalink names in team: the parent's
// thread when it names a reply. A permalink without thread_ts costs one
// conversations.replies call to find the parent.
func FollowUnit(ctx context.Context, c *slackapi.Client, team, permalink string) (unit.Unit, error) {
	conv, ts, threadTS, err := parsePermalink(permalink)
	if err != nil {
		return unit.Unit{}, err
	}
	if threadTS == "" {
		page, err := c.Replies(ctx, conv, ts, slackapi.PageArgs{Limit: 1})
		if err != nil {
			return unit.Unit{}, err
		}
		if len(page.Messages) == 0 {
			return unit.Unit{}, fmt.Errorf("slack: no message %s in %s", ts, conv)
		}
		m, err := dslack.MessageFrom(page.Messages[0])
		if err != nil {
			return unit.Unit{}, err
		}
		threadTS = m.Anchor()
	}
	u := dslack.ThreadUnit(team, conv, threadTS)
	if err := dslack.Codec().Valid(u.Key); err != nil {
		return unit.Unit{}, err
	}
	return u, nil
}

func parsePermalink(s string) (conv, ts, threadTS string, err error) {
	bad := fmt.Errorf("not a Slack message permalink: %s", s)
	u, perr := url.Parse(strings.TrimSpace(s))
	if perr != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", "", "", bad
	}
	if host := strings.ToLower(u.Hostname()); host != "slack.com" && !strings.HasSuffix(host, ".slack.com") {
		return "", "", "", bad
	}
	m := permalinkPath.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", "", bad
	}
	conv, ts = m[1], m[2]+"."+m[3]
	if t := u.Query().Get("thread_ts"); permalinkTS.MatchString(t) {
		threadTS = t
	}
	return conv, ts, threadTS, nil
}

// PermalinkUnit is the unit holding a permalink's message in team: the
// tracked DM segment or thread it lands in, else its thread.
func (l Landing) PermalinkUnit(ctx context.Context, c *slackapi.Client, team, permalink string) (unit.Unit, error) {
	conv, ts, threadTS, err := parsePermalink(permalink)
	if err != nil {
		return unit.Unit{}, err
	}
	if u, ok, err := l.UnitOf(ctx, team, conv, ts, threadTS); err != nil || ok {
		return u, err
	}
	return FollowUnit(ctx, c, team, permalink)
}
