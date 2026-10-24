package slack_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/slacktest"
)

const fakeAppToken = "xapp-0-fake-app-token"

func TestHelpersSendTheDocumentedArgs(t *testing.T) {
	t.Parallel()
	page := slack.PageArgs{Oldest: "1.0", Latest: "9.0", Cursor: "cur", Limit: 50, Inclusive: true}
	pageForm := url.Values{"oldest": {"1.0"}, "latest": {"9.0"}, "cursor": {"cur"}, "limit": {"50"}, "inclusive": {"true"}}
	with := func(base url.Values, extra url.Values) url.Values {
		out := url.Values{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		name   string
		method string
		answer any
		call   func(context.Context, *slack.Client) (any, error)
		args   url.Values
		auth   string
		want   any
	}{
		{
			name: "AuthTest", method: "auth.test",
			answer: map[string]any{"team_id": "T000", "team": "Team", "user_id": "U000", "url": "https://x.slack.com/"},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.AuthTest(ctx) },
			args:   url.Values{}, auth: fakeToken,
			want: slack.Identity{Team: "T000", TeamName: "Team", User: "U000", URL: "https://x.slack.com/"},
		},
		{
			name: "PostMessage", method: "chat.postMessage",
			answer: map[string]any{"channel": "C000", "ts": "1.2"},
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return c.PostMessage(ctx, slack.PostArgs{Channel: "C000", Text: "hi", ThreadTS: "1.0", Broadcast: true})
			},
			args: url.Values{"channel": {"C000"}, "text": {"hi"}, "thread_ts": {"1.0"}, "reply_broadcast": {"true"}}, auth: fakeToken,
			want: slack.Posted{Channel: "C000", TS: "1.2"},
		},
		{
			name: "PostMessage top level", method: "chat.postMessage",
			answer: map[string]any{"channel": "C000", "ts": "1.2"},
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return c.PostMessage(ctx, slack.PostArgs{Channel: "C000", Text: "hi"})
			},
			args: url.Values{"channel": {"C000"}, "text": {"hi"}}, auth: fakeToken,
			want: slack.Posted{Channel: "C000", TS: "1.2"},
		},
		{
			name: "Permalink", method: "chat.getPermalink",
			answer: map[string]any{"permalink": "https://x/p1"},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.Permalink(ctx, "C000", "1.2") },
			args:   url.Values{"channel": {"C000"}, "message_ts": {"1.2"}}, auth: fakeToken,
			want: "https://x/p1",
		},
		{
			name: "ConversationInfo", method: "conversations.info",
			answer: map[string]any{"channel": map[string]any{"id": "D000", "is_im": true, "user": "U001"}},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.ConversationInfo(ctx, "D000") },
			args:   url.Values{"channel": {"D000"}}, auth: fakeToken,
			want: slack.ConversationInfo{ID: "D000", IsIM: true, User: "U001"},
		},
		{
			name: "OpenDM", method: "conversations.open",
			answer: map[string]any{"channel": map[string]any{"id": "D000"}},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.OpenDM(ctx, "U001") },
			args:   url.Values{"users": {"U001"}}, auth: fakeToken,
			want: "D000",
		},
		{
			name: "Replies", method: "conversations.replies",
			answer: map[string]any{"messages": []any{map[string]any{"ts": "1.0"}}, "has_more": true, "response_metadata": map[string]any{"next_cursor": "next"}},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.Replies(ctx, "C000", "1.0", page) },
			args:   with(pageForm, url.Values{"channel": {"C000"}, "ts": {"1.0"}}), auth: fakeToken,
			want: slack.MessagesPage{Messages: raws(`{"ts":"1.0"}`), NextCursor: "next", HasMore: true},
		},
		{
			name: "History", method: "conversations.history",
			answer: map[string]any{"messages": []any{}},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.History(ctx, "C000", page) },
			args:   with(pageForm, url.Values{"channel": {"C000"}}), auth: fakeToken,
			want: slack.MessagesPage{Messages: raws()},
		},
		{
			name: "History empty page args", method: "conversations.history",
			answer: map[string]any{"messages": []any{}},
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return c.History(ctx, "C000", slack.PageArgs{})
			},
			args: url.Values{"channel": {"C000"}}, auth: fakeToken,
			want: slack.MessagesPage{Messages: raws()},
		},
		{
			name: "ListConversations", method: "conversations.list",
			answer: map[string]any{"channels": []any{map[string]any{"id": "C000", "name": "general", "is_channel": true, "is_member": true}}, "response_metadata": map[string]any{"next_cursor": "n"}},
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return c.ListConversations(ctx, "public_channel,private_channel", "cur")
			},
			args: url.Values{"types": {"public_channel,private_channel"}, "cursor": {"cur"}, "limit": {"200"}, "exclude_archived": {"true"}}, auth: fakeToken,
			want: slack.ConversationsPage{Conversations: []slack.ConversationInfo{{ID: "C000", Name: "general", IsChannel: true, IsMember: true}}, NextCursor: "n"},
		},
		{
			name: "UserInfo", method: "users.info",
			answer: map[string]any{"user": userJSON()},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.UserInfo(ctx, "U001") },
			args:   url.Values{"user": {"U001"}}, auth: fakeToken,
			want: wantUser(),
		},
		{
			name: "UserByEmail", method: "users.lookupByEmail",
			answer: map[string]any{"user": userJSON()},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.UserByEmail(ctx, "a@example.com") },
			args:   url.Values{"email": {"a@example.com"}}, auth: fakeToken,
			want: wantUser(),
		},
		{
			name: "ListUsers", method: "users.list",
			answer: map[string]any{"members": []any{userJSON()}, "response_metadata": map[string]any{"next_cursor": "n"}},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.ListUsers(ctx, "cur") },
			args:   url.Values{"cursor": {"cur"}, "limit": {"200"}}, auth: fakeToken,
			want: slack.UsersPage{Users: []slack.UserInfo{wantUser()}, NextCursor: "n"},
		},
		{
			name: "SearchMessages", method: "search.messages",
			answer: map[string]any{"messages": map[string]any{"matches": []any{map[string]any{"ts": "1.0"}}, "total": 1, "paging": map[string]any{"page": 2, "pages": 3}}},
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return c.SearchMessages(ctx, slack.SearchArgs{Query: "in:#x", SortDir: "asc", Page: 2, Count: 20})
			},
			args: url.Values{"query": {"in:#x"}, "sort": {"timestamp"}, "sort_dir": {"asc"}, "page": {"2"}, "count": {"20"}}, auth: fakeToken,
			want: slack.SearchPage{Matches: raws(`{"ts":"1.0"}`), Total: 1, Page: 2, Pages: 3},
		},
		{
			name: "ConnectionsOpen", method: "apps.connections.open",
			answer: map[string]any{"url": "wss://example/link"},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.ConnectionsOpen(ctx, fakeAppToken) },
			args:   url.Values{}, auth: fakeAppToken,
			want: "wss://example/link",
		},
		{
			name: "OAuthAccess code", method: "oauth.v2.access",
			answer: map[string]any{"team": map[string]any{"id": "T000"}, "authed_user": map[string]any{
				"id": "U000", "access_token": "xoxe.xoxp-1-new", "refresh_token": "xoxe-1-r", "expires_in": 43200, "scope": "chat:write"}},
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return c.OAuthAccess(ctx, slack.OAuthArgs{ClientID: "1.2", Code: "code", CodeVerifier: "ver", RedirectURI: "http://127.0.0.1:41234/slack/callback"})
			},
			args: url.Values{"client_id": {"1.2"}, "code": {"code"}, "code_verifier": {"ver"}, "redirect_uri": {"http://127.0.0.1:41234/slack/callback"}, "grant_type": {"authorization_code"}},
			want: slack.OAuthResponse{AccessToken: "xoxe.xoxp-1-new", RefreshToken: "xoxe-1-r", ExpiresIn: 43200, Scope: "chat:write", User: "U000", Team: "T000"},
		},
		{
			name: "OAuthAccess refresh", method: "oauth.v2.access",
			answer: map[string]any{"access_token": "xoxe.xoxp-1-top", "refresh_token": "xoxe-1-r2", "expires_in": 43200, "user_id": "U000", "team": map[string]any{"id": "T000"}},
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return c.OAuthAccess(ctx, slack.OAuthArgs{ClientID: "1.2", RefreshToken: "xoxe-1-r"})
			},
			args: url.Values{"client_id": {"1.2"}, "grant_type": {"refresh_token"}, "refresh_token": {"xoxe-1-r"}},
			want: slack.OAuthResponse{AccessToken: "xoxe.xoxp-1-top", RefreshToken: "xoxe-1-r2", ExpiresIn: 43200, User: "U000", Team: "T000"},
		},
		{
			name: "Revoke", method: "auth.revoke",
			call: func(ctx context.Context, c *slack.Client) (any, error) { return nil, c.Revoke(ctx) },
			args: url.Values{}, auth: fakeToken,
		},
		{
			name: "AddReaction", method: "reactions.add",
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return nil, c.AddReaction(ctx, "C000", "1.2", "eyes")
			},
			args: url.Values{"channel": {"C000"}, "timestamp": {"1.2"}, "name": {"eyes"}}, auth: fakeToken,
		},
		{
			name: "RemoveReaction", method: "reactions.remove",
			call: func(ctx context.Context, c *slack.Client) (any, error) {
				return nil, c.RemoveReaction(ctx, "C000", "1.2", "eyes")
			},
			args: url.Values{"channel": {"C000"}, "timestamp": {"1.2"}, "name": {"eyes"}}, auth: fakeToken,
		},
		{
			name: "Reactions", method: "reactions.get",
			answer: map[string]any{"type": "message", "message": map[string]any{"reactions": []any{map[string]any{"name": "eyes", "users": []any{"U001"}, "count": 1}}}},
			call:   func(ctx context.Context, c *slack.Client) (any, error) { return c.Reactions(ctx, "C000", "1.2") },
			args:   url.Values{"channel": {"C000"}, "timestamp": {"1.2"}, "full": {"true"}}, auth: fakeToken,
			want: []slack.Reaction{{Name: "eyes", Users: []string{"U001"}, Count: 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, fake := newClient(t)
			fake.Handle(tc.method, func(url.Values) any { return tc.answer })
			got, err := tc.call(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != nil && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("result = %#v\nwant     %#v", got, tc.want)
			}
			calls := fake.Calls(tc.method)
			if len(calls) != 1 {
				t.Fatalf("requests = %d, want 1", len(calls))
			}
			if !reflect.DeepEqual(calls[0], tc.args) {
				t.Fatalf("args = %v\nwant   %v", calls[0], tc.args)
			}
			if auth := fake.Authorization(tc.method)[0]; auth != tc.auth {
				t.Fatalf("bearer = %q, want %q", auth, tc.auth)
			}
		})
	}
}

func TestAuthTestReadsScopes(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	fake.Handle("auth.test", func(url.Values) any {
		return slacktest.Response{Header: http.Header{"X-OAuth-Scopes": {"a, b"}}, Body: map[string]any{"user_id": "U000"}}
	})
	id, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(id.Scopes, []string{"a", "b"}) {
		t.Fatalf("Scopes = %q", id.Scopes)
	}
}

func TestOAuthResponseNeverPrintsTokens(t *testing.T) {
	t.Parallel()
	r := slack.OAuthResponse{AccessToken: "xoxe.xoxp-1-secret", RefreshToken: "xoxe-1-secret", User: "U000"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := sprintf(verb, r); strings.Contains(got, "secret") {
			t.Fatalf("%s printed %q", verb, got)
		}
	}
}

func userJSON() map[string]any {
	return map[string]any{
		"id": "U001", "name": "handle", "real_name": "Real", "deleted": false, "is_bot": false, "tz": "America/Sao_Paulo",
		"profile": map[string]any{"display_name": "Disp", "real_name": "Real", "email": "a@example.com"},
	}
}

func wantUser() slack.UserInfo {
	return slack.UserInfo{ID: "U001", Name: "handle", RealName: "Real", DisplayName: "Disp", Email: "a@example.com", TZ: "America/Sao_Paulo"}
}

func raws(objs ...string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(objs))
	for _, o := range objs {
		out = append(out, json.RawMessage(o))
	}
	return out
}

func sprintf(verb string, v any) string { return fmt.Sprintf(verb, v) }
