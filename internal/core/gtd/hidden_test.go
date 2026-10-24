package gtd_test

import (
	"context"
	"errors"
	"testing"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestADisabledPluginsEntriesLeaveTheViewsAndCountsButStayDismissable(t *testing.T) {
	r := newRig()
	var off []unit.ProviderID
	r.svc = coregtd.New(coregtd.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
		Disabled: func() []unit.ProviderID { return off }})
	ctx := context.Background()
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	r.put(t, gtd.Entry{ID: "e-gh", Kind: gtd.KindMessage, State: gtd.Inbox, Units: []unit.Unit{unitU}, Origin: gtd.OriginMessage})
	r.put(t, gtd.Entry{ID: "e-next", Kind: gtd.KindAction, State: gtd.Next, Units: []unit.Unit{unitV}, Origin: gtd.OriginUser})
	r.put(t, gtd.Entry{ID: "e-wait", Kind: gtd.KindAction, State: gtd.Waiting, Units: []unit.Unit{unitV}, Origin: gtd.OriginUser})
	r.put(t, gtd.Entry{ID: "e-both", Kind: gtd.KindAction, State: gtd.Inbox, Units: []unit.Unit{unitU, slack}, Origin: gtd.OriginUser})
	r.put(t, gtd.Entry{ID: "e-mine", Kind: gtd.KindAction, State: gtd.Inbox, Origin: gtd.OriginUser})
	ids := func(v gtd.View) []gtd.ID {
		t.Helper()
		es, err := r.svc.Entries(ctx, gtd.Query{View: v})
		if err != nil {
			t.Fatal(err)
		}
		var out []gtd.ID
		for _, e := range es {
			out = append(out, e.ID)
		}
		return out
	}
	counts := func() gtd.Counters {
		t.Helper()
		c, err := r.svc.Counts(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := counts(); c.Badge != 3 || c.Views[gtd.ViewNext] != 1 {
		t.Fatalf("enabled: %+v", c)
	}
	off, r.facts.off[unit.GitHub] = []unit.ProviderID{unit.GitHub}, true
	if got := ids(gtd.ViewInbox); len(got) != 2 || got[0] == "e-gh" || got[1] == "e-gh" {
		t.Fatalf("inbox %v", got)
	}
	if got := ids(gtd.ViewNext); len(got) != 0 {
		t.Fatalf("next %v", got)
	}
	if got := ids(gtd.ViewWaiting); len(got) != 1 {
		t.Fatalf("waiting keeps it read-only: %v", got)
	}
	c := counts()
	if c.Badge != 2 || c.Views[gtd.ViewInbox] != 2 || c.Views[gtd.ViewNext] != 0 || c.Views[gtd.ViewWaiting] != 1 {
		t.Fatalf("counts %+v", c)
	}
	p, err := r.svc.EntriesSince(ctx, gtd.Query{View: gtd.ViewInbox}, coregtd.Since{})
	if err != nil || len(p.Entries) != 2 || p.Counters.Badge != 2 {
		t.Fatalf("page %+v %v", p, err)
	}
	if _, err := r.svc.Do(ctx, "e-gh", coregtd.Request{Verb: gtd.VerbNext}); !errors.Is(err, coregtd.ErrPluginDisabled) {
		t.Fatalf("next: %v", err)
	}
	if res, err := r.svc.Do(ctx, "e-gh", coregtd.Request{Verb: gtd.VerbDismiss}); err != nil || res.Entry.State != gtd.Trash {
		t.Fatalf("dismiss: %+v %v", res, err)
	}
	if _, err := r.svc.Do(ctx, "e-both", coregtd.Request{Verb: gtd.VerbDismiss}); !errors.Is(err, coregtd.ErrPluginDisabled) {
		t.Fatalf("dismiss of an entry with a unit of an enabled plugin: %v", err)
	}
	if got := r.entry(t, "e-next"); got.State != gtd.Next {
		t.Fatalf("data kept: %+v", got)
	}
	off, r.facts.off[unit.GitHub] = nil, false
	if got := ids(gtd.ViewNext); len(got) != 1 {
		t.Fatalf("enabled again: %v", got)
	}
}
