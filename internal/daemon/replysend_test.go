package daemon

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// draftReplies is the bundled profile with the reply verb's send set to draft.
func draftReplies(t *testing.T) func() *intentload.Loaded {
	t.Helper()
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.Load(profile.BundledChain(), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	l.Tasks.Verbs = maps.Clone(l.Tasks.Verbs)
	v := l.Tasks.Verbs["reply"]
	v.Send = "draft"
	l.Tasks.Verbs["reply"] = v
	return func() *intentload.Loaded { return l }
}

func TestTheCatalogServesTheReplyVerbsSend(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	sendOf := func() map[string]string {
		var cat api.ViewsCatalog
		if code := get(t, r.s, api.ViewsPath, &cat); code != 200 {
			t.Fatalf("%d", code)
		}
		out := map[string]string{}
		for _, v := range cat.Verbs {
			out[v.Verb] = v.Send
		}
		return out
	}
	if got := sendOf(); got["reply"] != "now" || got["done"] != "" {
		t.Fatalf("bundled: %v", got)
	}
	r.s.deps.Intents = draftReplies(t)
	if got := sendOf(); got["reply"] != "draft" {
		t.Fatalf("a profile with send draft: %v", got)
	}
}

func TestANotificationOffersTheReplyItsProfileSendsFirst(t *testing.T) {
	t.Parallel()
	r, _, _ := newDelegateRig(t)
	r.s.deps.Outbox = &outbox.Service{Store: r.db.Drafts(), Clock: fixedClock(tasksNow)}
	r.s.deps.Intents = draftReplies(t)
	r.put(t, unitTask("t-a", task.OriginDerived, actThread))
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-a"), &v); code != 200 || v.Attention == nil {
		t.Fatalf("%d %+v", code, v.Attention)
	}
	var ids []string
	for _, a := range v.Attention.Actions {
		ids = append(ids, a.ID)
	}
	if want := []string{api.AttentionDraftForMe, api.AttentionReply, api.AttentionSendReply}; !slices.Equal(ids, want) {
		t.Fatalf("actions %v, want %v", ids, want)
	}
}
