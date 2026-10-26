package slack

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

type Identity struct {
	Team, TeamName, User, URL string
	Scopes                    []string
}

type PostArgs struct {
	Channel, Text, ThreadTS string
	Broadcast               bool
}

type Posted struct{ Channel, TS string }

type PageArgs struct {
	Oldest, Latest, Cursor string
	Limit                  int
	Inclusive              bool
}

type MessagesPage struct {
	Messages   []json.RawMessage
	NextCursor string
	HasMore    bool
}

type SearchArgs struct {
	Query, Sort, SortDir string
	Page, Count          int
}

// SearchPage holds the raw matches of search.messages; each carries a channel object besides the message fields.
type SearchPage struct {
	Matches            []json.RawMessage
	Total, Page, Pages int
}

type ConversationInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsChannel  bool   `json:"is_channel"`
	IsGroup    bool   `json:"is_group"`
	IsIM       bool   `json:"is_im"`
	IsMPIM     bool   `json:"is_mpim"`
	IsPrivate  bool   `json:"is_private"`
	IsArchived bool   `json:"is_archived"`
	IsMember   bool   `json:"is_member"`
	User       string `json:"user"` // the other party of an im
}

type ConversationsPage struct {
	Conversations []ConversationInfo
	NextCursor    string
}

type UserInfo struct {
	ID, Name, RealName, DisplayName, Email, TZ string
	IsBot, Deleted                             bool
}

type UsersPage struct {
	Users      []UserInfo
	NextCursor string
}

type cursorMeta struct {
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

func (c *Client) AuthTest(ctx context.Context) (Identity, error) {
	var out struct {
		TeamID string `json:"team_id"`
		Team   string `json:"team"`
		UserID string `json:"user_id"`
		URL    string `json:"url"`
	}
	h, err := c.call(ctx, "auth.test", c.userToken, nil, &out)
	if err != nil {
		return Identity{}, err
	}
	id := Identity{Team: out.TeamID, TeamName: out.Team, User: out.UserID, URL: out.URL}
	for s := range strings.SplitSeq(h.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			id.Scopes = append(id.Scopes, s)
		}
	}
	return id, nil
}

func (c *Client) PostMessage(ctx context.Context, a PostArgs) (Posted, error) {
	args := url.Values{"channel": {a.Channel}, "text": {a.Text}}
	if a.ThreadTS != "" {
		args.Set("thread_ts", a.ThreadTS)
	}
	if a.Broadcast {
		args.Set("reply_broadcast", "true")
	}
	var out struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := c.Call(ctx, "chat.postMessage", args, &out); err != nil {
		return Posted{}, err
	}
	return Posted{Channel: out.Channel, TS: out.TS}, nil
}

// UpdateMessage replaces the text of the message at ts in channel (chat.update).
func (c *Client) UpdateMessage(ctx context.Context, channel, ts, text string) error {
	return c.Call(ctx, "chat.update", url.Values{"channel": {channel}, "ts": {ts}, "text": {text}}, nil)
}

func (c *Client) Permalink(ctx context.Context, channel, ts string) (string, error) {
	var out struct {
		Permalink string `json:"permalink"`
	}
	err := c.Call(ctx, "chat.getPermalink", url.Values{"channel": {channel}, "message_ts": {ts}}, &out)
	return out.Permalink, err
}

func (c *Client) ConversationInfo(ctx context.Context, id string) (ConversationInfo, error) {
	var out struct {
		Channel ConversationInfo `json:"channel"`
	}
	err := c.Call(ctx, "conversations.info", url.Values{"channel": {id}}, &out)
	return out.Channel, err
}

func (c *Client) OpenDM(ctx context.Context, user string) (string, error) {
	var out struct {
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
	}
	err := c.Call(ctx, "conversations.open", url.Values{"users": {user}}, &out)
	return out.Channel.ID, err
}

func (c *Client) Replies(ctx context.Context, channel, ts string, a PageArgs) (MessagesPage, error) {
	args := a.values()
	args.Set("channel", channel)
	args.Set("ts", ts)
	return c.messages(ctx, "conversations.replies", args)
}

func (c *Client) History(ctx context.Context, channel string, a PageArgs) (MessagesPage, error) {
	args := a.values()
	args.Set("channel", channel)
	return c.messages(ctx, "conversations.history", args)
}

func (a PageArgs) values() url.Values {
	v := url.Values{}
	setIf(v, "oldest", a.Oldest)
	setIf(v, "latest", a.Latest)
	setIf(v, "cursor", a.Cursor)
	if a.Limit > 0 {
		v.Set("limit", strconv.Itoa(a.Limit))
	}
	if a.Inclusive {
		v.Set("inclusive", "true")
	}
	return v
}

func (c *Client) messages(ctx context.Context, method string, args url.Values) (MessagesPage, error) {
	var out struct {
		Messages []json.RawMessage `json:"messages"`
		HasMore  bool              `json:"has_more"`
		cursorMeta
	}
	if err := c.Call(ctx, method, args, &out); err != nil {
		return MessagesPage{}, err
	}
	return MessagesPage{Messages: out.Messages, NextCursor: out.ResponseMetadata.NextCursor, HasMore: out.HasMore}, nil
}

// ListConversations pages conversations.list, archived ones excluded; types is Slack's
// comma-separated list ("public_channel,private_channel,mpim,im").
func (c *Client) ListConversations(ctx context.Context, types, cursor string) (ConversationsPage, error) {
	args := url.Values{"limit": {"200"}, "exclude_archived": {"true"}}
	setIf(args, "types", types)
	setIf(args, "cursor", cursor)
	var out struct {
		Channels []ConversationInfo `json:"channels"`
		cursorMeta
	}
	if err := c.Call(ctx, "conversations.list", args, &out); err != nil {
		return ConversationsPage{}, err
	}
	return ConversationsPage{Conversations: out.Channels, NextCursor: out.ResponseMetadata.NextCursor}, nil
}

type wireUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"real_name"`
	TZ       string `json:"tz"`
	IsBot    bool   `json:"is_bot"`
	Deleted  bool   `json:"deleted"`
	Profile  struct {
		DisplayName string `json:"display_name"`
		RealName    string `json:"real_name"`
		Email       string `json:"email"`
	} `json:"profile"`
}

func (w wireUser) info() UserInfo {
	realName := w.RealName
	if realName == "" {
		realName = w.Profile.RealName
	}
	return UserInfo{ID: w.ID, Name: w.Name, RealName: realName, DisplayName: w.Profile.DisplayName,
		Email: w.Profile.Email, TZ: w.TZ, IsBot: w.IsBot, Deleted: w.Deleted}
}

func (c *Client) UserInfo(ctx context.Context, id string) (UserInfo, error) {
	return c.user(ctx, "users.info", url.Values{"user": {id}})
}

func (c *Client) UserByEmail(ctx context.Context, email string) (UserInfo, error) {
	return c.user(ctx, "users.lookupByEmail", url.Values{"email": {email}})
}

func (c *Client) user(ctx context.Context, method string, args url.Values) (UserInfo, error) {
	var out struct {
		User wireUser `json:"user"`
	}
	if err := c.Call(ctx, method, args, &out); err != nil {
		return UserInfo{}, err
	}
	return out.User.info(), nil
}

func (c *Client) ListUsers(ctx context.Context, cursor string) (UsersPage, error) {
	args := url.Values{"limit": {"200"}}
	setIf(args, "cursor", cursor)
	var out struct {
		Members []wireUser `json:"members"`
		cursorMeta
	}
	if err := c.Call(ctx, "users.list", args, &out); err != nil {
		return UsersPage{}, err
	}
	page := UsersPage{Users: make([]UserInfo, 0, len(out.Members)), NextCursor: out.ResponseMetadata.NextCursor}
	for _, m := range out.Members {
		page.Users = append(page.Users, m.info())
	}
	return page, nil
}

// SearchMessages sorts by timestamp unless a.Sort says otherwise.
func (c *Client) SearchMessages(ctx context.Context, a SearchArgs) (SearchPage, error) {
	sort := a.Sort
	if sort == "" {
		sort = "timestamp"
	}
	args := url.Values{"query": {a.Query}, "sort": {sort}}
	setIf(args, "sort_dir", a.SortDir)
	if a.Page > 0 {
		args.Set("page", strconv.Itoa(a.Page))
	}
	if a.Count > 0 {
		args.Set("count", strconv.Itoa(a.Count))
	}
	var out struct {
		Messages struct {
			Matches []json.RawMessage `json:"matches"`
			Total   int               `json:"total"`
			Paging  struct {
				Page  int `json:"page"`
				Pages int `json:"pages"`
			} `json:"paging"`
		} `json:"messages"`
	}
	if err := c.Call(ctx, "search.messages", args, &out); err != nil {
		return SearchPage{}, err
	}
	m := out.Messages
	return SearchPage{Matches: m.Matches, Total: m.Total, Page: m.Paging.Page, Pages: m.Paging.Pages}, nil
}

// ConnectionsOpen calls apps.connections.open with the app-level token as bearer.
func (c *Client) ConnectionsOpen(ctx context.Context, appToken string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if _, err := c.call(ctx, "apps.connections.open", fixedToken(appToken), nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// Revoke revokes the user token (auth.revoke).
func (c *Client) Revoke(ctx context.Context) error {
	return c.Call(ctx, "auth.revoke", nil, nil)
}

func setIf(v url.Values, key, value string) {
	if value != "" {
		v.Set(key, value)
	}
}
