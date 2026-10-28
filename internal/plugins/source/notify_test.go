package source_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

func TestAHostTopicNotifiesTheSubscribedPlugin(t *testing.T) {
	log := change.NewLog(change.DefaultWindow, nil)
	k := kindOver(t, log)
	acme, beta := &listener{Stub: sourcetest.NewStub()}, &listener{Stub: sourcetest.NewStub()}
	start(t, k, "acme", "notify = [\"turns\", \"rounds\"]\n", acme)
	start(t, k, "beta", "notify = [\"rounds\"]\n", beta)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	from := log.Gen()
	wg.Go(func() { k.Notify(ctx, log, from) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	one, two := unit.Unit{Provider: "acme", Key: "one"}, unit.Unit{Provider: "beta", Key: "two"}
	log.Note(port.TopicTurns, one, two)
	eventually(t, "acme hears of its turn", func() bool { return len(acme.Notified()) == 1 })
	if got := acme.Notified()[0]; !slices.Equal(got, []plugin.Unit{{Provider: "acme", Key: "one"}}) {
		t.Errorf("acme heard of %v, want its own unit only", got)
	}
	if got := acme.heard()[0]; !slices.Equal(got, []plugin.Topic{plugin.TopicTurns}) {
		t.Errorf("acme heard topics %v", got)
	}

	log.Note(port.TopicPause)
	eventually(t, "both hear of the pause", func() bool { return len(acme.Notified()) == 2 && len(beta.Notified()) == 1 })
	if got := beta.heard()[0]; !slices.Equal(got, []plugin.Topic{plugin.TopicRounds}) || len(beta.Notified()[0]) != 0 {
		t.Errorf("beta heard %v of %v, want rounds of no unit", got, beta.Notified()[0])
	}

	log.Note(port.TopicRounds, one)
	eventually(t, "acme hears of its round", func() bool { return len(acme.Notified()) == 3 })
	log.Note(port.TopicTurns, two)
	log.Note(port.TopicPause)
	eventually(t, "beta hears of the second pause", func() bool { return len(beta.Notified()) == 2 })
	eventually(t, "acme hears of the second pause", func() bool { return len(acme.Notified()) >= 4 })
	if n := len(beta.heard()); n != 2 {
		t.Errorf("beta, subscribed to rounds only, heard %d notifications: %v", n, beta.heard())
	}
	for _, ts := range beta.heard() {
		if slices.Contains(ts, plugin.TopicTurns) {
			t.Errorf("beta heard of turns: %v", beta.heard())
		}
	}
}
