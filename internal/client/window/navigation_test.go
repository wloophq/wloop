package window

import "testing"

func TestNavigationSetsOnlyTheHashOnTheSamePage(t *testing.T) {
	load, hash := Navigation("http://127.0.0.1:7000/?token=a#view=inbox", "http://127.0.0.1:7000/?token=a#task=t-1")
	if load || hash != "task=t-1" {
		t.Fatalf("got (%v, %q), want (false, task=t-1)", load, hash)
	}
}

func TestNavigationReloadsWhenTheOriginOrTokenChanges(t *testing.T) {
	for _, c := range []struct{ name, current, next string }{
		{"port", "http://127.0.0.1:7000/?token=a#view=inbox", "http://127.0.0.1:7001/?token=a#view=inbox"},
		{"token", "http://127.0.0.1:7000/?token=a", "http://127.0.0.1:7000/?token=b#task=t-1"},
		{"nothing loaded", "", "http://127.0.0.1:7000/?token=a"},
	} {
		if load, _ := Navigation(c.current, c.next); !load {
			t.Errorf("%s: kept the page %q for %q", c.name, c.current, c.next)
		}
	}
}

func TestNavigationKeepsThePlaceForAnEmptyRoute(t *testing.T) {
	load, hash := Navigation("http://127.0.0.1:7000/?token=a#task=t-1", "http://127.0.0.1:7000/?token=a")
	if load || hash != "" {
		t.Fatalf("got (%v, %q), want (false, \"\")", load, hash)
	}
}
