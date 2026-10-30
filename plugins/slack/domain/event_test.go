package slack

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const team = "T00000001"

var at = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

type recorded struct {
	Type    string `json:"type"`
	Payload struct {
		EventID string `json:"event_id"`
		Event   struct {
			Type, Subtype, User, Channel string
			BotID                        string          `json:"bot_id"`
			AppID                        string          `json:"app_id"`
			ThreadTS                     string          `json:"thread_ts"`
			Username                     string          `json:"username"`
			BotProfile                   json.RawMessage `json:"bot_profile"`
			Message                      json.RawMessage `json:"message"`
		} `json:"event"`
	} `json:"payload"`
	raw json.RawMessage
}

func recordings(t *testing.T) []recorded {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "listen.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []recorded
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var r recorded
		var raw struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		r.raw = raw.Payload
		out = append(out, r)
	}
	if len(out) == 0 || len(out) >= 200 {
		t.Fatalf("%d recorded envelopes", len(out))
	}
	return out
}

func TestDeltasFromRecordings(t *testing.T) {
	var golden bytes.Buffer
	kinds := map[Kind]int{}
	agentPosts := 0
	for i, r := range recordings(t) {
		if r.Type != "events_api" {
			continue
		}
		d, ok, err := FromEvent(team, r.raw, ViaSocket, at)
		if err != nil {
			t.Fatalf("envelope %d: %v", i, err)
		}
		line, _ := json.Marshal(struct {
			OK    bool  `json:"ok"`
			Delta Delta `json:"delta"`
		}{ok, d})
		golden.Write(append(line, '\n'))

		ev := r.Payload.Event
		hidden := ev.Type == "message" && slices.Contains(HiddenSubtypes, ev.Subtype)
		if ok == hidden {
			t.Fatalf("envelope %d (%s/%s): ok=%v", i, ev.Type, ev.Subtype, ok)
		}
		if !ok {
			continue
		}
		kinds[d.Kind]++
		if d.EventID != r.Payload.EventID || d.Via != ViaSocket || !d.ReceivedAt.Equal(at) || d.Conversation == "" || d.TS == "" {
			t.Fatalf("envelope %d: %+v", i, d)
		}
		want := map[string]Kind{"message_changed": KindEdit, "message_deleted": KindDelete}[ev.Subtype]
		switch {
		case ev.Type == "reaction_added":
			want = KindReactionAdded
		case want == "":
			want = KindMessage
		}
		if ev.Subtype == "message_changed" && bytes.Contains(r.Payload.Event.Message, []byte(`"tombstone"`)) {
			want = KindDelete
		}
		if d.Kind != want {
			t.Fatalf("envelope %d (%s/%s): kind %s, want %s", i, ev.Type, ev.Subtype, d.Kind, want)
		}
		if (d.Kind == KindReactionAdded) != (d.Message == nil) || (d.Kind == KindReactionAdded) != (d.Reaction != nil) {
			t.Fatalf("envelope %d: message %v reaction %v", i, d.Message, d.Reaction)
		}
		if d.Kind == KindMessage {
			m := d.Message
			if d.TS != m.TS || d.ThreadTS != ev.ThreadTS || m.User != ev.User || m.BotID != ev.BotID {
				t.Fatalf("envelope %d: delta %+v message %+v", i, d, m)
			}
			if ev.Username != "" && m.Username != ev.Username {
				t.Fatalf("envelope %d: username %q", i, m.Username)
			}
			if ev.AppID != "" && m.AppID != ev.AppID {
				t.Fatalf("envelope %d: app id %q", i, m.AppID)
			}
			if ev.User != "" && ev.BotID != "" && ev.BotProfile != nil {
				agentPosts++
				if m.AppID == "" || m.Username == "" {
					t.Fatalf("envelope %d: agent post without app or name: %+v", i, m)
				}
			}
		}
	}
	for _, k := range []Kind{KindMessage, KindEdit, KindDelete, KindReactionAdded} {
		if kinds[k] == 0 {
			t.Errorf("no %s delta in the recording", k)
		}
	}
	if agentPosts == 0 {
		t.Error("no user+bot_id post in the recording")
	}

	path := filepath.Join("testdata", "deltas.golden.jsonl")
	if *update {
		if err := os.WriteFile(path, golden.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, golden.Bytes()) {
		t.Fatalf("deltas differ from %s; rerun with -update and review the diff", path)
	}
}

func TestFromEventCases(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		ok      bool
		want    Delta
	}{
		{
			name:    "reply from an agent app",
			payload: `{"event_id":"Ev1","event":{"type":"message","channel":"C1","channel_type":"channel","user":"U000000001","bot_id":"B1","bot_profile":{"app_id":"A1","name":"agent"},"text":"hi <@U000000002> see <https://x.test/a|a>","ts":"2.0","thread_ts":"1.0","parent_user_id":"U000000003"}}`,
			ok:      true,
			want: Delta{Conversation: "C1", ChannelType: "channel", TS: "2.0", ThreadTS: "1.0", EventID: "Ev1", Kind: KindMessage,
				Message: &Message{TS: "2.0", ThreadTS: "1.0", ParentUser: "U000000003", User: "U000000001", BotID: "B1", AppID: "A1", Username: "agent",
					Text: "hi <@U000000002> see <https://x.test/a|a>", Links: []string{"https://x.test/a"}, Mentions: []string{"U000000002"}}},
		},
		{
			name:    "reaction removed",
			payload: `{"event_id":"Ev2","event":{"type":"reaction_removed","user":"U000000001","reaction":"eyes","item":{"type":"message","channel":"D1","channel_type":"im","ts":"1.0"},"item_user":"U000000002"}}`,
			ok:      true,
			want: Delta{Conversation: "D1", ChannelType: "im", TS: "1.0", EventID: "Ev2", Kind: KindReactionRemoved,
				Reaction: &struct{ Name, User string }{"eyes", "U000000001"}},
		},
		{
			name:    "deleted reply",
			payload: `{"event_id":"Ev3","event":{"type":"message","subtype":"message_deleted","channel":"C1","channel_type":"channel","deleted_ts":"2.0","previous_message":{"type":"message","user":"U000000001","text":"x","ts":"2.0","thread_ts":"1.0"}}}`,
			ok:      true,
			want: Delta{Conversation: "C1", ChannelType: "channel", TS: "2.0", ThreadTS: "1.0", EventID: "Ev3", Kind: KindDelete,
				Message: &Message{TS: "2.0", ThreadTS: "1.0", User: "U000000001", Text: "x", Deleted: true}},
		},
		{
			name:    "edit",
			payload: `{"event_id":"Ev4","event":{"type":"message","subtype":"message_changed","channel":"G1","channel_type":"group","message":{"type":"message","user":"U000000001","text":"y","ts":"2.0","edited":{"ts":"3.0"}},"previous_message":{"user":"U000000001","text":"x","ts":"2.0"}}}`,
			ok:      true,
			want: Delta{Conversation: "G1", ChannelType: "group", TS: "2.0", EventID: "Ev4", Kind: KindEdit,
				Message: &Message{TS: "2.0", User: "U000000001", Text: "y", EditedTS: "3.0"}},
		},
		{
			name:    "deleted parent left as a tombstone",
			payload: `{"event_id":"Ev5","event":{"type":"message","subtype":"message_changed","channel":"C1","message":{"type":"message","subtype":"tombstone","user":"USLACKBOT","text":"deleted","ts":"1.0","thread_ts":"1.0","reply_count":2,"latest_reply":"5.0"}}}`,
			ok:      true,
			want: Delta{Conversation: "C1", TS: "1.0", ThreadTS: "1.0", EventID: "Ev5", Kind: KindDelete,
				Message: &Message{TS: "1.0", ThreadTS: "1.0", User: "USLACKBOT", Subtype: "tombstone", Text: "deleted", Deleted: true, ReplyCount: 2, LatestReply: "5.0"}},
		},
		{name: "hidden subtype", payload: `{"event":{"type":"message","subtype":"channel_join","channel":"C1","user":"U000000001","ts":"1.0"}}`},
		{name: "message_replied", payload: `{"event":{"type":"message","subtype":"message_replied","channel":"C1","message":{"ts":"1.0"}}}`},
		{name: "non-message event", payload: `{"event":{"type":"app_mention","channel":"C1","user":"U000000001","ts":"1.0"}}`},
		{name: "reaction on a file", payload: `{"event":{"type":"reaction_added","user":"U000000001","reaction":"eyes","item":{"type":"file","file":"F1"}}}`},
		{name: "no event", payload: `{"type":"event_callback"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, ok, err := FromEvent(team, json.RawMessage(c.payload), ViaCatchUp, at)
			if err != nil || ok != c.ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if !ok {
				return
			}
			c.want.Via, c.want.ReceivedAt = ViaCatchUp, at
			if !reflect.DeepEqual(d, c.want) {
				t.Fatalf("got  %+v %+v\nwant %+v %+v", d, d.Message, c.want, c.want.Message)
			}
		})
	}
	if _, _, err := FromEvent(team, json.RawMessage(`{"event":`), ViaSocket, at); err == nil {
		t.Fatal("malformed payload accepted")
	}
}

func TestMessageFromMatchesFromEvent(t *testing.T) {
	compared := 0
	for i, r := range recordings(t) {
		if r.Type != "events_api" || r.Payload.Event.Type != "message" {
			continue
		}
		d, ok, err := FromEvent(team, r.raw, ViaSocket, at)
		if err != nil || !ok || d.Kind == KindDelete {
			continue
		}
		var p struct {
			Event json.RawMessage `json:"event"`
		}
		_ = json.Unmarshal(r.raw, &p)
		page := p.Event
		if d.Kind == KindEdit {
			page = r.Payload.Event.Message
		}
		m, err := MessageFrom(page)
		if err != nil {
			t.Fatalf("envelope %d: %v", i, err)
		}
		if !reflect.DeepEqual(m, *d.Message) {
			t.Fatalf("envelope %d:\nhistory %+v\nsocket  %+v", i, m, *d.Message)
		}
		compared++
	}
	if compared < 20 {
		t.Fatalf("compared only %d messages", compared)
	}

	event := `{"event_id":"Ev9","event":{"type":"message","subtype":"file_share","channel":"C1","channel_type":"channel",` + historyFields + `}}`
	history := `{"type":"message","subtype":"file_share","permalink":"","reactions":[],` + historyFields + `}`
	d, ok, err := FromEvent(team, json.RawMessage(event), ViaSocket, at)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	m, err := MessageFrom(json.RawMessage(history))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, *d.Message) {
		t.Fatalf("history %+v\nsocket  %+v", m, *d.Message)
	}
	want := Message{TS: "5.0", ThreadTS: "4.0", ParentUser: "U000000009", User: "U000000001", Subtype: "file_share",
		Text: "ping <@U000000002|bob> and <@U000000003>, <https://a.test/x> <mailto:a@b.test|mail> <@U000000002>", EditedTS: "6.0",
		Files:    []File{{ID: "F1", Title: "plan", Mimetype: "text/plain"}},
		Links:    []string{"https://a.test/x", "https://b.test/y", "https://c.test/z"},
		Mentions: []string{"U000000002", "U000000003"}, ReplyCount: 0}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got  %+v\nwant %+v", m, want)
	}

	search, err := MessageFrom(json.RawMessage(`{"type":"message","user":"U000000001","username":"al","ts":"7.0","text":"x","channel":{"id":"C1","name":"general"},"permalink":"https://t.slack.test/archives/C1/p7","reactions":[{"name":"eyes","users":["U000000002"],"count":1}],"reply_count":3,"latest_reply":"8.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if search.Permalink == "" || search.Username != "al" || len(search.Reactions) != 1 || search.Reactions[0].Users[0] != "U000000002" || search.ReplyCount != 3 || search.LatestReply != "8.0" {
		t.Fatalf("search match %+v", search)
	}
	if _, err := MessageFrom(json.RawMessage(`[`)); err == nil {
		t.Fatal("malformed message accepted")
	}
}

const historyFields = `"user":"U000000001","ts":"5.0","thread_ts":"4.0","parent_user_id":"U000000009","edited":{"ts":"6.0"},
"text":"ping <@U000000002|bob> and <@U000000003>, <https://a.test/x> <mailto:a@b.test|mail> <@U000000002>",
"files":[{"id":"F1","title":"plan","mimetype":"text/plain"}],
"attachments":[{"title_link":"https://b.test/y","from_url":"https://c.test/z","original_url":"https://c.test/z"}]`
