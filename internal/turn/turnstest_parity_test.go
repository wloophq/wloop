package turn

import (
	"reflect"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns/turnstest"
)

func TestTheCoresTestRegistryMatchesTheBundledManifests(t *testing.T) {
	want, got := bundled(t).Registry, turnstest.Intents()
	if !reflect.DeepEqual(got.Answerable(), want.Answerable()) {
		t.Errorf("Answerable: test registry %v, bundled %v", got.Answerable(), want.Answerable())
	}
	if !reflect.DeepEqual(got.SessionTitles(), want.SessionTitles()) {
		t.Errorf("SessionTitles: test registry %v, bundled %v", got.SessionTitles(), want.SessionTitles())
	}
	for _, d := range want.All() {
		n := string(d.Name)
		gr, gerr := got.Rules(n)
		wr, werr := want.Rules(n)
		if gerr != nil || werr != nil || !reflect.DeepEqual(gr, wr) {
			t.Errorf("Rules(%s): test registry %+v (%v)\nbundled %+v (%v)", n, gr, gerr, wr, werr)
		}
		if got.ClosesWhenDone(n) != want.ClosesWhenDone(n) {
			t.Errorf("ClosesWhenDone(%s) differs", n)
		}
		if got.Reconcile(n) != want.Reconcile(n) {
			t.Errorf("Reconcile(%s): test registry %q, bundled %q", n, got.Reconcile(n), want.Reconcile(n))
		}
	}
	if len(got.All()) != len(want.All()) {
		t.Errorf("test registry holds %d intents, bundled %d", len(got.All()), len(want.All()))
	}
}
