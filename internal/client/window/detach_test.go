package window

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/api"
)

func request(stream chan api.Change, action, route string) {
	stream <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: action, Route: route}}}
}

func TestDetachShowsTheItemInItsOwnWindow(t *testing.T) {
	for _, c := range []struct{ route, id, detached string }{
		{"view=inbox&task=t-1&layout=board", "task=t-1", "task=t-1&layout=detail"},
		{"task=t-1&layout=detail", "task=t-1", "task=t-1&layout=detail"},
		{"view=needs-me&unit=github/acme%2Fapi%238", "unit=github/acme%2Fapi%238", "unit=github/acme%2Fapi%238&layout=detail"},
		{"unit=github/acme%2Fapi%231&task=t-2", "task=t-2", "task=t-2&layout=detail"},
	} {
		synctest.Test(t, func(t *testing.T) {
			stream := make(chan api.Change)
			r := newRig(stream)
			r.run(t)
			synctest.Wait()
			request(stream, api.WindowDetach, c.route)
			synctest.Wait()
			want(t, r.host.taken(), "detached "+c.id+" "+base+"#"+c.detached)
		})
	}
}

// One window per item: every route of the same item names the same window,
// which the host focuses instead of opening a second.
func TestDetachingTheSameItemAgainNamesTheSameWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		request(stream, api.WindowDetach, "view=inbox&task=t-1")
		request(stream, api.WindowDetach, "task=t-1&ctx=home")
		synctest.Wait()
		want(t, r.host.taken(), "detached task=t-1 "+base+"#task=t-1&layout=detail", "detached task=t-1 "+base+"#task=t-1&layout=detail")
	})
}

func TestDetachWithoutAnItemOpensTheMainWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		request(stream, api.WindowDetach, "view=inbox&task=")
		synctest.Wait()
		want(t, r.host.taken(), "main "+base+"#view=inbox&task=", "hide-panel")
	})
}

func TestTheMainWindowNeverTakesTheDetailLayout(t *testing.T) {
	r := newRig()
	if err := r.ctl.Show("task=t-1&layout=detail"); err != nil {
		t.Fatal(err)
	}
	if err := r.ctl.Show("layout=detail"); err != nil {
		t.Fatal(err)
	}
	if err := r.ctl.Show("view=inbox&layout=board"); err != nil {
		t.Fatal(err)
	}
	want(t, r.host.taken(), "main "+base+"#task=t-1", "hide-panel", "main "+base, "hide-panel", "main "+base+"#view=inbox&layout=board", "hide-panel")
}

func TestAnOpenedLinkShowsItsRouteInTheMainWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(make(chan api.Change))
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnOpenURL(link("://open/#task=t-1"))
		want(t, r.host.taken(), "main "+base+"#task=t-1", "hide-panel")
	})
}

func TestAMalformedLinkOpensTheDefaultRouteAndSaysSo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(make(chan api.Change))
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnOpenURL(link("://elsewhere/#task=t-1"))
		want(t, r.host.taken(), "main "+base+"#"+MalformedLinkRoute, "hide-panel")
		if got := r.logs.all(); !strings.Contains(got, link("://elsewhere/#task=t-1")) {
			t.Errorf("the log %q does not name the link", got)
		}
	})
}
