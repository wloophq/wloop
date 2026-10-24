package eval

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	slackadapter "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

const (
	exTeam   = "T0000000001"
	exChan   = "C0000000001"
	exDM     = "D0000000001"
	exViewer = "U0000000001"
	exOther  = "U0000000002"
)

var exportNow = time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func exTS(n int) string { return fmt.Sprintf("%d.000100", 1792500000+n) }

func exportStore(t *testing.T) ExportOptions {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	segs := slackadapter.Segments{Store: db.Segments(), Clock: fixedClock{exportNow}}
	if _, err := segs.Apply(ctx, exTeam, exDM, []dslack.Span{{FromTS: exTS(100)}}); err != nil {
		t.Fatal(err)
	}
	st := &slackadapter.Store{DB: db.Slack(), Segments: segs, Now: func() time.Time { return exportNow }}
	msg := func(n int, user, text string) dslack.Message {
		return dslack.Message{TS: exTS(n), User: user, Text: text}
	}
	reply := func(n, parent int, user, text string) dslack.Message {
		m := msg(n, user, text)
		m.ThreadTS = exTS(parent)
		return m
	}
	first := msg(1, exOther, "<@U0000000001> please review the widget plan")
	first.Mentions = []string{exViewer}
	first.Reactions = []dslack.Reaction{{Name: "eyes", Users: []string{exViewer}}}
	facts := dslack.Facts{
		Team: exTeam,
		Conversations: []dslack.Conversation{
			{ID: exChan, Name: "widget-team", Kind: dslack.KindChannel, IsMember: true},
			{ID: exDM, Kind: dslack.KindIM, IsMember: true, DMUser: exOther},
		},
		Threads: []dslack.Thread{
			{Conversation: exChan, TS: exTS(1), LatestReply: exTS(2), ReplyCount: 1, Messages: []dslack.Message{first, reply(2, 1, exOther, "also cc the docs team")}},
			{Conversation: exChan, TS: exTS(10), Messages: []dslack.Message{msg(10, exOther, "standup notes")}},
		},
		DMs: []dslack.DMWindow{{Conversation: exDM, Latest: exTS(101), Messages: []dslack.Message{
			msg(100, exOther, "can you send the budget?"), msg(101, exViewer, "tomorrow"),
		}}},
		Users: []dslack.User{{ID: exViewer, Name: "vera", DisplayName: "Vera"}, {ID: exOther, Name: "ann"}},
	}
	if err := st.Save(ctx, nil, facts, "", exportNow); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{slackadapter.CursorViewerTeam: exTeam, slackadapter.CursorViewerUser: exViewer} {
		if err := db.Slack().SetCursor(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return ExportOptions{DB: db.Slack(), Facts: st, Segments: segs, Since: time.Unix(1792500000, 0), Until: time.Unix(1792500000+1000, 0),
		Out: t.TempDir(), Now: exportNow}
}

func TestExportFromTheStore(t *testing.T) {
	o := exportStore(t)
	rep, err := Export(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written != 3 || rep.Unsegmented != 0 {
		t.Fatalf("report %+v", rep)
	}
	set := loadSet(t, o.Out)
	if len(set.Fixtures) != 3 || set.Viewer.ID != exViewer || set.Viewer.Name != "Vera" || set.Users[exOther] != "ann" {
		t.Fatalf("set %+v", set)
	}
	kinds := map[string]int{}
	for _, f := range set.Fixtures {
		kinds[f.Kind]++
		if f.Split != SplitHoldout || f.Now != exportNow.Format(time.RFC3339) {
			t.Errorf("%s: split %q now %q", f.ID, f.Split, f.Now)
		}
		if _, err := f.NowAt(""); err != nil {
			t.Error(err)
		}
		switch f.Kind {
		case "dm_segment":
			if f.ChannelType != "im" || len(f.Messages) != 2 {
				t.Errorf("dm %+v", f)
			}
		case "mention":
			if len(f.Messages) != 2 || f.Messages[0].UserName != "ann" || len(f.Messages[0].Reactions) != 1 || f.Root() != exTS(1) {
				t.Errorf("mention %+v", f)
			}
		}
	}
	if kinds["thread"] != 1 || kinds["dm_segment"] != 1 || kinds["mention"] != 1 {
		t.Errorf("kinds %v", kinds)
	}
}

func TestExportExcludesSeenConversations(t *testing.T) {
	o := exportStore(t)
	seen := t.TempDir()
	f := Fixture{ID: "01-seen", Kind: "thread", ChannelID: exChan, ChannelType: "channel",
		Messages: []Message{{TS: exTS(2), ThreadTS: exTS(1), UserID: exOther}}}
	if err := writeJSON(filepath.Join(seen, f.ID+".json"), f); err != nil {
		t.Fatal(err)
	}
	dm := Fixture{ID: "02-seen-dm", Kind: "dm_segment", ChannelID: exDM, ChannelType: "im",
		Messages: []Message{{TS: exTS(99), UserID: exOther}, {TS: exTS(101), UserID: exViewer}}}
	if err := writeJSON(filepath.Join(seen, dm.ID+".json"), dm); err != nil {
		t.Fatal(err)
	}
	o.Exclude = []string{seen}
	rep, err := Export(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written != 1 || rep.Excluded != 2 {
		t.Fatalf("report %+v", rep)
	}
	for _, f := range loadSet(t, o.Out).Fixtures {
		if f.ChannelID == exChan && f.Root() == exTS(1) || f.ChannelID == exDM {
			t.Errorf("a seen conversation was exported: %s", f.Kind)
		}
	}
}
