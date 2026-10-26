package slack

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	slackapi "github.com/wloophq/wloop/internal/slack"
)

// SearchQuery is what slack_search looks for: Query as Slack's search
// reads it, narrowed by the filters set.
type SearchQuery struct {
	Query string
	// Channel is a #name or a channel id.
	Channel string
	// From is an @handle, a user id or an email.
	From string
	// After and Before are dates, YYYY-MM-DD, both exclusive.
	After, Before string
	Limit         int
}

type foundMessage struct {
	TS        string `json:"ts"`
	User      string `json:"user"`
	Username  string `json:"username"`
	Text      string `json:"text"`
	Permalink string `json:"permalink"`
	Channel   struct {
		ID     string `json:"id"`
		IsIM   bool   `json:"is_im"`
		IsMPIM bool   `json:"is_mpim"`
	} `json:"channel"`
}

// Search renders the newest messages the user can read that match q, one
// line each: permalink, author, time in r.Location and first line. A
// direct message is shown only when tracked, as slack_read reads it.
func (r *Reader) Search(ctx context.Context, q SearchQuery) (string, error) {
	query, err := r.searchQuery(ctx, q)
	if err != nil {
		return "", err
	}
	page, err := r.Client.SearchMessages(ctx, slackapi.SearchArgs{Query: query, Sort: "timestamp", SortDir: "desc", Count: q.Limit})
	if err != nil {
		return "", err
	}
	var found []foundMessage
	untracked := 0
	for _, raw := range page.Matches {
		var m foundMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return "", fmt.Errorf("slack search match: %w", err)
		}
		if m.Channel.IsIM || m.Channel.IsMPIM {
			units, err := r.Segments.UnitsFor(ctx, m.Channel.ID, m.TS)
			if err != nil {
				return "", err
			}
			if len(units) == 0 {
				untracked++
				continue
			}
		}
		found = append(found, m)
	}
	ids := make([]string, 0, len(found))
	for _, m := range found {
		if m.User != "" {
			ids = append(ids, m.User)
		}
	}
	users, err := r.Facts.Users(ctx, ids)
	if err != nil {
		return "", err
	}
	loc := cmp.Or(r.Location, time.Local)
	var b strings.Builder
	matches := "matches"
	if page.Total == 1 {
		matches = "match"
	}
	fmt.Fprintf(&b, "%d of %d %s for %s", len(found), page.Total, matches, query)
	for _, m := range found {
		author := m.Username + " (bot)"
		if m.User != "" {
			author = label(m.User, users)
		}
		line, _, _ := strings.Cut(strings.TrimSpace(m.Text), "\n")
		if line == "" {
			line = "(no text)"
		}
		when := m.TS
		if t := tsTime(m.TS); !t.IsZero() {
			when = t.In(loc).Format("2006-01-02 15:04 MST")
		}
		link := cmp.Or(m.Permalink, fmt.Sprintf("no permalink (channel %s, ts %s)", m.Channel.ID, m.TS))
		fmt.Fprintf(&b, "\n%s · %s · %s · %s", link, author, when, line)
	}
	if untracked > 0 {
		are := "are"
		if untracked == 1 {
			are = "is"
		}
		fmt.Fprintf(&b, "\n(%d direct %s that %s not tracked left out)", untracked, plural(untracked, "message"), are)
	}
	return b.String(), nil
}

// searchQuery is q as search.messages reads it: the query, then in:, from:,
// after: and before: for the filters set.
func (r *Reader) searchQuery(ctx context.Context, q SearchQuery) (string, error) {
	parts := []string{strings.TrimSpace(q.Query)}
	switch c := strings.TrimSpace(q.Channel); {
	case c == "":
	case strings.HasPrefix(c, "#"):
		parts = append(parts, "in:"+c)
	case conversationR.MatchString(c):
		info, err := r.Client.ConversationInfo(ctx, c)
		if err != nil {
			return "", err
		}
		parts = append(parts, "in:#"+info.Name)
	default:
		return "", fmt.Errorf("channel %q is not a #channel or a channel id", c)
	}
	switch f := strings.TrimSpace(q.From); {
	case f == "":
	case userOf(f) != "":
		parts = append(parts, "from:<@"+userOf(f)+">")
	case strings.HasPrefix(f, "@"):
		parts = append(parts, "from:"+f)
	case strings.Contains(f, "@"):
		u, err := r.Client.UserByEmail(ctx, f)
		if err != nil {
			return "", fmt.Errorf("looking up %s: %w", f, err)
		}
		parts = append(parts, "from:<@"+u.ID+">")
	default:
		return "", fmt.Errorf("from %q is not an @handle, a user id or an email", f)
	}
	if q.After != "" {
		parts = append(parts, "after:"+q.After)
	}
	if q.Before != "" {
		parts = append(parts, "before:"+q.Before)
	}
	return strings.Join(parts, " "), nil
}
