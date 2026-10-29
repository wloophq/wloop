package cli

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

func TestTheBundledNoticesTellTheAgentHowToReadAnAnswer(t *testing.T) {
	n := profileNotices{&Env{}}
	got, err := n.Notice(turns.NoticeAnswered, map[string]any{"Gate": int64(5), "Unit": "chat:C1/1700000000.000100", "Command": "'/bin/" + brand.Command + "'"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gate 5", "`task_read`", "`'/bin/" + brand.Command + "' turn answer chat:C1/1700000000.000100 --gate 5`"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	got, err = n.Notice(turns.NoticeRecordOwed, map[string]any{"Unit": "chat:C1/1", "Turn": "abc", "ScratchDir": ".loop", "Command": brand.Command})
	if err != nil || !strings.Contains(got, "`"+brand.Command+" turn end chat:C1/1 --turn abc --file .loop/turn-abc.json`") {
		t.Fatalf("%q %v", got, err)
	}
}

func TestTheProfileWordsEveryNoticeTheCoreSends(t *testing.T) {
	notices, problems := intentload.BundledNotices(turn.BundledFS())
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	for _, name := range []string{turns.NoticeAnswered, turns.NoticeRecordOwed} {
		if _, ok := builtin.NoticeOf(name); !ok || notices[name] == "" {
			t.Errorf("notice %s is not in the grammar or the profile", name)
		}
	}
}

func TestTheChatNoticeNamesTheCommandToASessionWithoutTheTool(t *testing.T) {
	n := profileNotices{&Env{}}
	data := map[string]any{"Task": "t-1", "Unit": "task:t-1", "Command": "'/bin/" + brand.Command + "'"}
	for capable, wants := range map[bool][]string{
		true:  {"`task_read`", "`'/bin/" + brand.Command + "' turn answer task:t-1`"},
		false: {"`'/bin/" + brand.Command + "' turn answer task:t-1`"},
	} {
		data["Capable"] = capable
		got, err := n.Notice(builtin.NoticeChat, data)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("capable %v: %q lacks %q", capable, got, want)
			}
		}
		if !capable && strings.Contains(got, "task_read") {
			t.Errorf("a session without the tool is told to call it: %q", got)
		}
	}
}
