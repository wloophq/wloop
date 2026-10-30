package outbox

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// taking is a publisher that posts to the targets takes accepts.
type taking struct {
	*fakeEffects
	takes func(target string) bool
}

func (t taking) Takes(target string) bool { return t.takes(target) }

var taskUnit = unit.Unit{Provider: unit.Task, Key: "e1"}

// byTarget wires a chat publisher that takes "#" targets, a plain one for
// pull requests, and no publisher for tasks.
func byTarget(e *env) (chat, prs *fakeEffects) {
	chat, prs = &fakeEffects{}, &fakeEffects{}
	pubs := map[unit.ProviderID]port.Effects{
		"chat":      taking{chat, func(t string) bool { return strings.HasPrefix(t, "#") }},
		unit.GitHub: prs,
	}
	e.svc.Effects = func(p unit.ProviderID) (port.Effects, error) {
		if fx, ok := pubs[p]; ok {
			return fx, nil
		}
		return nil, fmt.Errorf("%s: %w", p, ErrDisabled)
	}
	e.svc.Targets = []unit.ProviderID{unit.GitHub, "chat"}
	return chat, prs
}

func onTask(target string) Proposal {
	p := reply("shipped")
	p.Unit, p.Target = taskUnit, target
	return p
}

func TestADraftOnATaskIsPublishedByTheProviderOfItsTarget(t *testing.T) {
	e := newEnv()
	chat, _ := byTarget(e)
	d := e.propose(onTask("#general"))
	if got := e.svc.PublisherOf(d); got != "chat" {
		t.Fatalf("PublisherOf = %s", got)
	}
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, failed, err := e.svc.Send(ctx); sent != 1 || failed != 0 || err != nil {
		t.Fatalf("Send = %d, %d, %v", sent, failed, err)
	}
	if !slices.Equal(chat.published, []draft.ID{d.ID}) || e.get(d.ID).State != draft.Sent {
		t.Fatalf("published %v, draft %+v", chat.published, e.get(d.ID))
	}
}

func TestADraftOnATaskStuckSendingIsVerifiedByItsTargetsProvider(t *testing.T) {
	e := newEnv()
	chat, _ := byTarget(e)
	chat.found = true
	d := e.propose(onTask("#general"))
	d.State = draft.Sending
	if _, err := e.store.Update(ctx, d, draft.Held); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(chat.verified, []draft.ID{d.ID}) || e.get(d.ID).State != draft.Sent {
		t.Fatalf("verified %v, draft %+v", chat.verified, e.get(d.ID))
	}
}

func TestADraftOnATaskWhoseTargetNoProviderTakesIsRefused(t *testing.T) {
	e := newEnv()
	byTarget(e)
	for _, target := range []string{"", "ana@acme.example"} {
		if _, err := e.svc.Propose(ctx, onTask(target)); !errors.Is(err, ErrDisabled) {
			t.Errorf("target %q: %v", target, err)
		}
	}
}

func TestAProviderThatPublishesKeepsItsDraftsWhateverTheirTarget(t *testing.T) {
	e := newEnv()
	chat, prs := byTarget(e)
	p := reply("see #general")
	p.Unit, p.Target = pull, "#general"
	d := e.propose(p)
	if got := e.svc.PublisherOf(d); got != unit.GitHub {
		t.Fatalf("PublisherOf = %s", got)
	}
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if len(chat.published) != 0 || !slices.Equal(prs.published, []draft.ID{d.ID}) {
		t.Fatalf("chat %v, prs %v", chat.published, prs.published)
	}
}

func TestATargetTwoProvidersTakeNamesNeither(t *testing.T) {
	e := newEnv()
	chat, _ := byTarget(e)
	inner := e.svc.Effects
	e.svc.Effects = func(p unit.ProviderID) (port.Effects, error) {
		if p == "mail" {
			return taking{&fakeEffects{}, func(string) bool { return true }}, nil
		}
		return inner(p)
	}
	e.svc.Targets = append(e.svc.Targets, "mail")
	if _, err := e.svc.Propose(ctx, onTask("#general")); !errors.Is(err, ErrDisabled) {
		t.Fatalf("propose: %v", err)
	}
	if len(chat.published) != 0 {
		t.Fatal("published")
	}
}
