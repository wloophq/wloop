package cli

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type heldPlugin struct{ release chan struct{} }

func (h heldPlugin) Identity(context.Context) (src.Identity, error) {
	<-h.release
	return src.Identity{}, src.ErrUnavailable
}

func (h heldPlugin) BriefInput(context.Context, plugin.Unit) (src.BriefInput, error) {
	<-h.release
	return src.BriefInput{}, errors.New("released")
}

func TestServesStopWaitsForTheViewerRead(t *testing.T) {
	stopWaitsFor(t, func(e *Env) {
		e.withGitHubBroker(&psource.Broker{}, string(unit.GitHub), nil).Note(port.TopicViewer)
	})
}

func TestServesStopWaitsForABriefRead(t *testing.T) {
	stopWaitsFor(t, func(e *Env) {
		cut, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.withGitHubBriefs(brief.Deps{}).Source.(*briefInputs).Facts(cut, pr.Ref{Owner: "acme", Repo: "api", Number: 7}); err == nil {
			t.Fatal("a cut read answered")
		}
	})
}

// stopWaitsFor checks that the Env's stop waits on the plugin read start begins, until the read ends.
func stopWaitsFor(t *testing.T, start func(*Env)) {
	synctest.Test(t, func(t *testing.T) {
		held := heldPlugin{release: make(chan struct{})}
		e := (&Env{}).owning()
		e.identityReader = func() (identityReader, error) { return held, nil }
		e.briefReader = func() (briefReader, error) { return held, nil }
		start(e)

		stopped := make(chan struct{})
		go func() {
			e.background.Wait()
			close(stopped)
		}()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("stop returned while the read ran")
		default:
		}
		close(held.release)
		synctest.Wait()
		select {
		case <-stopped:
		default:
			t.Fatal("stop still waits after the read ended")
		}
	})
}
