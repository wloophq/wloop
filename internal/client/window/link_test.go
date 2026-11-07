package window

import "testing"

func TestLinkRouteReadsTheRouteOfALink(t *testing.T) {
	for _, c := range []struct{ link, route string }{
		{"wloop://open/#task=t-1", "task=t-1"},
		{"wloop://open#view=needs-me", "view=needs-me"},
		{"WLOOP://open/#unit=github/acme%2Fapi%238", "unit=github/acme%2Fapi%238"},
		{"wloop://open/", ""},
		{"wloop://open/#", ""},
		{" wloop://open/#view=inbox&q=a%20b\n", "view=inbox&q=a%20b"},
	} {
		route, ok := LinkRoute(c.link)
		if !ok || route != c.route {
			t.Errorf("LinkRoute(%q) = (%q, %v), want (%q, true)", c.link, route, ok, c.route)
		}
	}
}

func TestLinkRouteRefusesAMalformedLink(t *testing.T) {
	for _, link := range []string{
		"",
		"wloop://",
		"wloop://other/#task=t-1",
		"wloop://open/task#task=t-1",
		"wloop://open/?token=a#task=t-1",
		"wloop://me@open/#task=t-1",
		"wloop:open#task=t-1",
		"http://open/#task=t-1",
		"wloop://open/#task=%zz",
	} {
		if route, ok := LinkRoute(link); ok {
			t.Errorf("LinkRoute(%q) read %q", link, route)
		}
	}
}

func TestIsLinkNamesTheSchemeOnly(t *testing.T) {
	for link, want := range map[string]bool{
		"wloop://open/#task=t-1": true,
		"wloop://other":          true,
		"Wloop:whatever":         true,
		"#task=t-1":              false,
		"https://example.test/":  false,
		"wloopx://open/":         false,
	} {
		if got := IsLink(link); got != want {
			t.Errorf("IsLink(%q) = %v, want %v", link, got, want)
		}
	}
}

func TestInsetTitleBarMarksThePageAndKeepsTheRoute(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"http://127.0.0.1:7000/?token=a#task=t-1", "http://127.0.0.1:7000/?token=a&titlebar=inset#task=t-1"},
		{"http://127.0.0.1:7000/?token=a", "http://127.0.0.1:7000/?token=a&titlebar=inset"},
		{"http://127.0.0.1:7000/#view=inbox", "http://127.0.0.1:7000/?titlebar=inset#view=inbox"},
		{"http://127.0.0.1:7000/?token=a&titlebar=inset#x", "http://127.0.0.1:7000/?token=a&titlebar=inset#x"},
	} {
		if got := InsetTitleBar(c.in); got != c.want {
			t.Errorf("InsetTitleBar(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
