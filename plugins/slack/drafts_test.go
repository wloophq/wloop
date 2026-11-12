package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/pelletier/go-toml/v2"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/draft"
	"github.com/wloophq/wloop/plugins/slack/send"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// newLiveSlack is newPubSlack holding msgs, whose writes change what its
// reads answer: a Verify after a Publish sees the change, as one after a
// crash would.
func newLiveSlack(t *testing.T, msgs ...map[string]any) *pubSlack {
	t.Helper()
	ps := newPubSlack(t)
	ps.stored(msgs...)
	api := ps.f.api
	note := func(m string) {
		ps.mu.Lock()
		ps.order = append(ps.order, m)
		ps.mu.Unlock()
	}
	read := func(method string) func(url.Values) any {
		return func(v url.Values) any {
			note(method)
			ps.mu.Lock()
			defer ps.mu.Unlock()
			out := make([]map[string]any, len(ps.history))
			for i, m := range ps.history {
				out[i] = maps.Clone(m)
			}
			return map[string]any{"messages": out}
		}
	}
	api.Handle("conversations.history", read("conversations.history"))
	api.Handle("conversations.replies", read("conversations.replies"))
	api.Handle("chat.update", func(v url.Values) any {
		note("chat.update")
		ps.mu.Lock()
		defer ps.mu.Unlock()
		for _, m := range ps.history {
			if m["ts"] == v.Get("ts") {
				m["text"] = v.Get("text")
			}
		}
		return nil
	})
	api.Handle("chat.delete", func(v url.Values) any {
		note("chat.delete")
		ps.mu.Lock()
		defer ps.mu.Unlock()
		ps.history = slices.DeleteFunc(slices.Clone(ps.history), func(m map[string]any) bool { return m["ts"] == v.Get("ts") })
		return nil
	})
	api.Handle("reactions.remove", func(v url.Values) any {
		note("reactions.remove")
		ps.mu.Lock()
		defer ps.mu.Unlock()
		for _, r := range ps.reacted {
			users := r["users"].([]string)
			if r["name"] == v.Get("name") && slices.Contains(users, me) {
				r["users"] = slices.DeleteFunc(slices.Clone(users), func(u string) bool { return u == me })
				return nil
			}
		}
		return map[string]any{"ok": false, "error": "no_reaction"}
	})
	api.Handle("reactions.get", func(url.Values) any {
		note("reactions.get")
		ps.mu.Lock()
		defer ps.mu.Unlock()
		out := make([]map[string]any, len(ps.reacted))
		for i, r := range ps.reacted {
			out[i] = maps.Clone(r)
		}
		return map[string]any{"message": map[string]any{"reactions": out}}
	})
	return ps
}

func (ps *pubSlack) reactions(rs ...map[string]any) {
	ps.mu.Lock()
	ps.reacted = rs
	ps.mu.Unlock()
}

func posted(ts, user, text string) map[string]any {
	return map[string]any{"ts": ts, "thread_ts": pubParent, "user": user, "text": text}
}

func messageRef(ts string) string { return pubConv + "/" + ts }

func messageLink(ts string) string {
	return "https://acme.slack.com/archives/" + pubConv + "/p" + strings.ReplaceAll(ts, ".", "")
}

func changeDraft(kind draft.Kind, target, text string) draft.Draft {
	d := threadDraft(kind, text)
	d.Target = target
	return d
}

var verifyStart = time.Unix(1700000300, 0)

func TestPublishEditUpdates(t *testing.T) {
	ctx := context.Background()
	ps := newLiveSlack(t, posted(pubReply, me, "On it"))
	d := changeDraft(draft.Edit, messageRef(pubReply), "Done by **noon**, @{slack:U000000AAA}")
	ev, err := ps.publisher().Publish(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	updates := ps.calls("chat.update")
	if len(updates) != 1 || updates[0].Get("channel") != pubConv || updates[0].Get("ts") != pubReply ||
		updates[0].Get("text") != "Done by *noon*, <@U000000AAA>" {
		t.Fatalf("chat.update %v", updates)
	}
	if ev.Ref != messageRef(pubReply) || ev.Permalink != messageLink(pubReply) || len(ps.calls("chat.postMessage")) != 0 {
		t.Fatalf("evidence %+v, posts %d", ev, len(ps.calls("chat.postMessage")))
	}
	if where, err := ps.publisher().Where(ctx, d); err != nil || where != "#general" {
		t.Fatalf("Where = %q %v", where, err)
	}

	for name, tc := range map[string]struct {
		d       draft.Draft
		allow   []string
		mine    bool
		wantErr string
	}{
		"another's message": {d: d, wantErr: "not the user's"},
		"no target":         {d: changeDraft(draft.Edit, "", "x"), mine: true, wantErr: "message ref or permalink"},
		"a channel target":  {d: changeDraft(draft.Edit, pubConv, "x"), mine: true, wantErr: "message ref or permalink"},
		"empty text":        {d: changeDraft(draft.Edit, messageRef(pubReply), " "), mine: true, wantErr: "empty"},
		"a broadcast":       {d: changeDraft(draft.Edit, messageRef(pubReply), "<!here> done"), mine: true, wantErr: "broadcast"},
		"the allowlist":     {d: d, allow: []string{pubOutside}, mine: true, wantErr: send.ErrNotAllowed.Error()},
		"a deleted message": {d: changeDraft(draft.Edit, messageRef(pubPosted), "x"), mine: true, wantErr: "no message"},
	} {
		t.Run(name, func(t *testing.T) {
			author := other
			if tc.mine {
				author = me
			}
			ps := newLiveSlack(t, posted(pubReply, author, "On it"))
			_, err := ps.publisher(tc.allow...).Publish(ctx, tc.d)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.HasPrefix(err.Error(), src.ErrMayHavePosted.Error()) {
				t.Fatalf("Publish = %v, want %q", err, tc.wantErr)
			}
			if n := len(ps.calls("chat.update")); n != 0 {
				t.Fatalf("updated %d", n)
			}
		})
	}
}

func TestPublishDeleteDeletes(t *testing.T) {
	ctx := context.Background()
	ps := newLiveSlack(t, posted(pubReply, me, "On it"))
	d := changeDraft(draft.Delete, messageLink(pubReply), "")
	ev, err := ps.publisher().Publish(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	deletes := ps.calls("chat.delete")
	if len(deletes) != 1 || deletes[0].Get("channel") != pubConv || deletes[0].Get("ts") != pubReply {
		t.Fatalf("chat.delete %v", deletes)
	}
	seen := ps.seen()
	if ev.Ref != messageRef(pubReply) || ev.Permalink == "" || slices.Index(seen, "chat.getPermalink") > slices.Index(seen, "chat.delete") {
		t.Fatalf("evidence %+v, calls %v: the permalink is read before the message goes", ev, seen)
	}

	for name, tc := range map[string]struct {
		d       draft.Draft
		author  string
		allow   []string
		wantErr string
	}{
		"another's message": {d: d, author: other, wantErr: "not the user's"},
		"no target":         {d: changeDraft(draft.Delete, "", ""), author: me, wantErr: "message ref or permalink"},
		"a person target":   {d: changeDraft(draft.Delete, other, ""), author: me, wantErr: "message ref or permalink"},
		"the allowlist":     {d: d, author: me, allow: []string{pubOutside}, wantErr: send.ErrNotAllowed.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			ps := newLiveSlack(t, posted(pubReply, tc.author, "On it"))
			if _, err := ps.publisher(tc.allow...).Publish(ctx, tc.d); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Publish = %v, want %q", err, tc.wantErr)
			}
			if n := len(ps.calls("chat.delete")); n != 0 {
				t.Fatalf("deleted %d", n)
			}
		})
	}
}

func TestPublishUnreactRemoves(t *testing.T) {
	ctx := context.Background()
	eyes := func() map[string]any { return map[string]any{"name": "eyes", "users": []string{other, me}, "count": 2} }
	ps := newLiveSlack(t)
	ps.reactions(eyes())
	d := threadDraft(draft.Unreact, "")
	d.Reaction = ":eyes:"
	ev, err := ps.publisher().Publish(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	removes := ps.calls("reactions.remove")
	if len(removes) != 1 || removes[0].Get("channel") != pubConv || removes[0].Get("timestamp") != pubParent || removes[0].Get("name") != "eyes" {
		t.Fatalf("reactions.remove %v", removes)
	}
	if ev.Ref != messageRef(pubParent) || len(ps.calls("reactions.add")) != 0 {
		t.Fatalf("evidence %+v", ev)
	}
	if _, err := ps.publisher().Publish(ctx, d); err != nil {
		t.Fatalf("removing a reaction already gone: %v", err)
	}

	ps = newLiveSlack(t)
	ps.reactions(eyes())
	d.Target = messageLink(pubReply)
	if _, err := ps.publisher().Publish(ctx, d); err != nil {
		t.Fatal(err)
	}
	if removes := ps.calls("reactions.remove"); len(removes) != 1 || removes[0].Get("timestamp") != pubReply {
		t.Fatalf("a permalink's reactions.remove %v", removes)
	}

	noScope := func() (auth.Token, error) { return auth.Token{Team: team, User: me}, nil }
	for name, tc := range map[string]struct {
		d       draft.Draft
		p       *Publisher
		wantErr string
	}{
		"no reaction":      {d: threadDraft(draft.Unreact, ""), p: ps.publisher(), wantErr: "emoji"},
		"a channel target": {d: draft.Draft{Unit: d.Unit, Kind: draft.Unreact, Delivery: draft.Unreaction, Reaction: "eyes", Target: pubConv}, p: ps.publisher(), wantErr: "message ref"},
		"no scope":         {d: d, p: ps.publisherWith(noScope), wantErr: reactionsScope},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(ps.calls("reactions.remove"))
			if err := tc.p.Validate(ctx, tc.d); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate = %v, want %q", err, tc.wantErr)
			}
			if _, err := tc.p.Publish(ctx, tc.d); err == nil || len(ps.calls("reactions.remove")) != before {
				t.Fatalf("Publish = %v", err)
			}
		})
	}
}

// verifyAround asserts Verify finds no change before Publish and finds it
// after: a crash between the call and its record settles either way.
func verifyAround(t *testing.T, ps *pubSlack, d draft.Draft) draft.Evidence {
	t.Helper()
	ctx := context.Background()
	if _, ok, err := ps.publisher().Verify(ctx, d, verifyStart); err != nil || ok {
		t.Fatalf("Verify before Publish = %v %v", ok, err)
	}
	if _, err := ps.publisher().Publish(ctx, d); err != nil {
		t.Fatal(err)
	}
	ev, ok, err := ps.publisher().Verify(ctx, d, verifyStart)
	if err != nil || !ok {
		t.Fatalf("Verify after Publish = %+v %v %v", ev, ok, err)
	}
	return ev
}

func TestVerifyEdit(t *testing.T) {
	ctx := context.Background()
	d := changeDraft(draft.Edit, messageRef(pubReply), "Done by [noon](https://x.y/1)")
	ps := newLiveSlack(t, posted(pubReply, me, "On it"))
	if ev := verifyAround(t, ps, d); ev.Ref != messageRef(pubReply) || ev.Permalink != messageLink(pubReply) {
		t.Fatalf("evidence %+v", ev)
	}

	for name, msgs := range map[string][]map[string]any{
		"another's message with the text": {posted(pubReply, other, "Done by <https://x.y/1|noon>")},
		"the message is gone":             nil,
	} {
		ps := newLiveSlack(t, msgs...)
		if _, ok, err := ps.publisher().Verify(ctx, d, verifyStart); err != nil || ok {
			t.Errorf("%s: Verify = %v %v", name, ok, err)
		}
	}

	ps = newLiveSlack(t, posted(pubReply, me, "On it"))
	ps.f.api.Handle("conversations.history", func(url.Values) any { return slacktest.Response{Status: http.StatusServiceUnavailable, Body: "x"} })
	if _, ok, err := ps.publisher().Verify(ctx, d, verifyStart); ok || err == nil || !strings.HasPrefix(err.Error(), src.ErrMayHavePosted.Error()+"; check") {
		t.Fatalf("a failed read: Verify = %v %v", ok, err)
	}
}

func TestVerifyDelete(t *testing.T) {
	ctx := context.Background()
	d := changeDraft(draft.Delete, messageRef(pubReply), "")
	ps := newLiveSlack(t, posted(pubParent, other, "the parent"), posted(pubReply, me, "On it"))
	if ev := verifyAround(t, ps, d); ev.Ref != messageRef(pubReply) {
		t.Fatalf("evidence %+v", ev)
	}

	tomb := posted(pubReply, "USLACKBOT", "This message was deleted.")
	tomb["subtype"] = "tombstone"
	ps = newLiveSlack(t, tomb)
	if _, ok, err := ps.publisher().Verify(ctx, d, verifyStart); err != nil || !ok {
		t.Fatalf("a deleted parent's tombstone: Verify = %v %v", ok, err)
	}

	ps = newLiveSlack(t, posted(pubReply, me, "On it"))
	ps.f.api.Handle("conversations.history", func(url.Values) any { return slacktest.Response{Status: http.StatusServiceUnavailable, Body: "x"} })
	if _, ok, err := ps.publisher().Verify(ctx, d, verifyStart); ok || err == nil || !strings.HasPrefix(err.Error(), src.ErrMayHavePosted.Error()+"; check") {
		t.Fatalf("a failed read: Verify = %v %v", ok, err)
	}
}

func TestVerifyUnreact(t *testing.T) {
	ctx := context.Background()
	d := threadDraft(draft.Unreact, "")
	d.Reaction = "eyes"
	ps := newLiveSlack(t)
	ps.reactions(map[string]any{"name": "eyes", "users": []string{other, me}, "count": 2})
	if ev := verifyAround(t, ps, d); ev.Ref != messageRef(pubParent) {
		t.Fatalf("evidence %+v", ev)
	}

	for name, rs := range map[string][]map[string]any{
		"another's reaction only":    {{"name": "eyes", "users": []string{other}, "count": 1}},
		"the user's other reaction":  {{"name": "tada", "users": []string{me}, "count": 1}},
		"no reaction on the message": nil,
	} {
		ps := newLiveSlack(t)
		ps.reactions(rs...)
		if _, ok, err := ps.publisher().Verify(ctx, d, verifyStart); err != nil || !ok {
			t.Errorf("%s: Verify = %v %v", name, ok, err)
		}
	}

	ps = newLiveSlack(t)
	ps.f.api.Handle("reactions.get", func(url.Values) any { return slacktest.Response{Status: http.StatusServiceUnavailable, Body: "x"} })
	if _, ok, err := ps.publisher().Verify(ctx, d, verifyStart); ok || err == nil || !strings.HasPrefix(err.Error(), src.ErrMayHavePosted.Error()) {
		t.Fatalf("a failed read: Verify = %v %v", ok, err)
	}
}

func TestSlackDraftKeepsMentionsAndCrossUnit(t *testing.T) {
	t.Parallel()
	f := newToolFixture(t, true)
	f.srv.Handle("users.list", func(url.Values) any {
		return map[string]any{"members": []map[string]any{{"id": "U001", "name": "dana"}}}
	})
	turnUnit := dslack.ThreadUnit(toolTeam, "C100", toolThread).Wire()
	otherUnit := dslack.ThreadUnit(toolTeam, "C100", toolOther).Wire()
	elsewhere := "C100/" + toolOther
	for _, tc := range []struct {
		args string
		want src.Draft
	}{
		{`{"text":"thanks @dana"}`, src.Draft{Unit: turnUnit, Kind: "reply", Delivery: "message", Text: "thanks @{slack:U001}"}},
		{`{"kind":"edit","target":"` + elsewhere + `","text":"ping @dana"}`,
			src.Draft{Unit: otherUnit, Kind: "edit", Delivery: "edit", Target: elsewhere, Text: "ping @{slack:U001}"}},
		{`{"kind":"unreact","target":"` + elsewhere + `","reaction":"eyes"}`,
			src.Draft{Unit: otherUnit, Kind: "unreact", Delivery: "unreaction", Target: elsewhere, Reaction: "eyes"}},
		{`{"kind":"delete","target":"C100/` + toolReply + `","text":"ignored"}`,
			src.Draft{Unit: turnUnit, Kind: "delete", Delivery: "delete", Target: "C100/" + toolReply}},
	} {
		before := len(f.h.Gates())
		r := f.call(t, ToolDraft, tc.args)
		gates := f.h.Gates()
		if r.Answer != "proposed" || r.IsError || len(gates) != before+1 {
			t.Fatalf("%s: %q %s", tc.args, r.Answer, r.Fields)
		}
		g := gates[before].Draft
		if g.Unit != tc.want.Unit || g.Kind != tc.want.Kind || g.Delivery != tc.want.Delivery || g.Purpose != tc.want.Kind ||
			g.Target != tc.want.Target || g.Text != tc.want.Text || g.Reaction != tc.want.Reaction || gates[before].Session != toolSession {
			t.Errorf("%s: proposed %+v, want %+v", tc.args, g, tc.want)
		}
	}

	before := len(f.h.Gates())
	for args, want := range map[string][]string{
		`{"kind":"edit","text":"hi"}`:                          {"problem_message"},
		`{"kind":"edit","target":"#general"}`:                  {"problem_message", "problem_text_required"},
		`{"kind":"delete"}`:                                    {"problem_message"},
		`{"kind":"unreact","target":"C100/` + toolReply + `"}`: {"problem_reaction_required"},
	} {
		r := f.call(t, ToolDraft, args)
		if got := toolProblems(t, r); r.Answer != "fix_these" || !slices.Equal(got, want) {
			t.Errorf("%s: %q %v, want %v", args, r.Answer, got, want)
		}
	}
	r := f.call(t, ToolDraft, `{"kind":"edit","target":"`+elsewhere+`","text":"x","items":["i1"]}`)
	if msg, _ := toolFields(t, r)["Error"].(string); r.Answer != "failed" || !strings.HasPrefix(msg, "items:") {
		t.Errorf("an edit elsewhere answering the turn's items: %q %s", r.Answer, r.Fields)
	}
	if n := len(f.h.Gates()); n != before {
		t.Fatalf("a refused draft opened a gate: %d", n-before)
	}
	if len(f.srv.Calls("chat.update"))+len(f.srv.Calls("chat.delete"))+len(f.srv.Calls("reactions.remove"))+len(f.srv.Calls("chat.postMessage")) != 0 {
		t.Fatal("slack_draft wrote to Slack")
	}
}

func TestSlackDraftPayloadHold(t *testing.T) {
	t.Parallel()
	f := newToolFixture(t, true)
	for i, tc := range []struct {
		args    string
		payload string
		basis   string
		hold    bool
	}{
		{`{"text":"Done","payload":{},"hold":true}`, `{}`, "", true},
		{`{"kind":"react","reaction":"eyes","hold":false}`, "", "", false},
		{`{"text":"Done","payload":{"k":"v"},"basis":"abc123"}`, `{"k":"v"}`, "abc123", false},
	} {
		if r := f.call(t, ToolDraft, tc.args); r.Answer != "proposed" {
			t.Fatalf("%s: %q %s", tc.args, r.Answer, r.Fields)
		}
		g := f.h.Gates()[i].Draft
		if string(g.Payload) != tc.payload || g.Basis != tc.basis || g.Hold != tc.hold {
			t.Errorf("%s: payload %s basis %q hold %t", tc.args, g.Payload, g.Basis, g.Hold)
		}
	}
	for _, args := range []string{`{"text":"x","hold":"yes"}`, `{"text":"x","payload":[1]}`, `{"text":"x","basis":7}`} {
		if r := f.call(t, ToolDraft, args); r.Answer != "bad_arguments" {
			t.Errorf("%s: %q", args, r.Answer)
		}
	}
	d := DraftOf(src.Draft{Payload: json.RawMessage(`{"k":"v"}`), Basis: "b", Hold: true})
	if back := WireDraft(d); string(back.Payload) != `{"k":"v"}` || back.Basis != "b" || !back.Hold {
		t.Fatalf("the plugin's draft drops payload, basis or hold: %+v", back)
	}
}

// TestEveryKindHasAPreview reads the manifest: slack_draft offers each kind
// it declares, and each kind's preview renders a line from the draft.
func TestEveryKindHasAPreview(t *testing.T) {
	var m struct {
		Source struct {
			DraftKinds []struct{ Name, Preview string } `toml:"draft_kinds"`
			Tools      []struct{ Name, Input string }   `toml:"tools"`
		} `toml:"source"`
	}
	raw, err := os.ReadFile("plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range m.Source.DraftKinds {
		names = append(names, k.Name)
		body, err := os.ReadFile(k.Preview)
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		tmpl, err := template.New(k.Name).Option("missingkey=error").Parse(string(body))
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		for _, d := range []src.Draft{{Kind: k.Name, Reaction: "eyes"}, {Kind: k.Name, Target: "C0123/1700000000.000100", Reaction: "eyes", Text: "hi"}} {
			var out strings.Builder
			if err := tmpl.Execute(&out, d); err != nil || strings.TrimSpace(out.String()) == "" || strings.Contains(out.String(), "\n") {
				t.Errorf("%s over %+v: %q %v", k.Name, d, out.String(), err)
			}
		}
	}
	var input struct {
		Properties struct {
			Kind struct{ Enum []string } `json:"kind"`
		} `json:"properties"`
	}
	for _, tool := range m.Source.Tools {
		if tool.Name == ToolDraft {
			if err := json.Unmarshal([]byte(tool.Input), &input); err != nil {
				t.Fatal(err)
			}
		}
	}
	slices.Sort(names)
	enum := slices.Sorted(slices.Values(input.Properties.Kind.Enum))
	offered := slices.Sorted(slices.Values(draftKinds))
	if !slices.Equal(names, enum) || !slices.Equal(names, offered) {
		t.Fatalf("kinds %v, slack_draft's enum %v, offered %v", names, enum, offered)
	}
}

// TestAtOnceToolsUnchanged compares each at-once tool's Slack calls, answer
// and recorded draft with testdata/at_once_tools.golden, written before
// drafts could edit, delete and unreact.
func TestAtOnceToolsUnchanged(t *testing.T) {
	t.Parallel()
	methods := []string{"conversations.info", "conversations.history", "conversations.replies", "chat.postMessage", "chat.update",
		"chat.delete", "reactions.add", "reactions.remove", "chat.getPermalink", "users.info", "users.list"}
	var out bytes.Buffer
	for _, c := range []struct{ tool, args string }{
		{ToolSend, `{"target":"C100/` + toolReply + `","text":"Shipping **today**, @dana","broadcast":true}`},
		{ToolSend, `{"target":"C100","text":"Top level"}`},
		{ToolReact, `{"message":"C100/` + toolReply + `","emoji":":eyes:"}`},
		{ToolReact, `{"message":"C100/` + toolReply + `","emoji":"eyes","remove":true}`},
		{ToolEdit, `{"message":"C100/` + toolReply + `","text":"On it, done by _noon_"}`},
		{ToolDelete, `{"message":"https://acme.slack.com/archives/C100/p1700000100000200"}`},
		{ToolEdit, `{"message":"C100/` + toolThread + `","text":"not mine"}`},
		{ToolEdit, `{"message":"C100/1700000777.000100","text":"gone"}`},
		{ToolDelete, `{"message":"C100/1700000777.000100"}`},
	} {
		f := newToolFixture(t, true)
		f.srv.Handle("chat.delete", func(url.Values) any { return map[string]any{} })
		f.srv.Handle("reactions.remove", func(url.Values) any { return map[string]any{} })
		f.srv.Handle("users.list", func(url.Values) any {
			return map[string]any{"members": []map[string]any{{"id": "U001", "name": "dana"}}}
		})
		r := f.call(t, c.tool, c.args)
		fmt.Fprintf(&out, "== %s %s\nanswer %s error=%t %s\n", c.tool, c.args, r.Answer, r.IsError, r.Fields)
		for _, m := range methods {
			for _, v := range f.srv.Calls(m) {
				fmt.Fprintf(&out, "call %s %s\n", m, formOf(v))
			}
		}
		for _, s := range f.h.SentDrafts() {
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&out, "recorded %s\n", raw)
		}
		if n := len(f.h.Gates()); n != 0 {
			t.Errorf("%s opened %d gates", c.tool, n)
		}
	}
	golden := filepath.Join("testdata", "at_once_tools.golden")
	if *updateGolden {
		if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != string(want) {
		t.Fatalf("the at-once tools changed:\n%s\nwant:\n%s", got, want)
	}
}

func formOf(v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + strings.Join(v[k], ",")
	}
	return strings.Join(parts, " ")
}
