package window

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

// link is a link of the app's scheme followed by rest.
func link(rest string) string { return brand.URLScheme() + rest }

func TestLinkRouteReadsTheRouteOfALink(t *testing.T) {
	for _, c := range []struct{ link, route string }{
		{link("://open/#task=t-1"), "task=t-1"},
		{link("://open#view=needs-me"), "view=needs-me"},
		{strings.ToUpper(brand.URLScheme()) + "://open/#unit=github/acme%2Fapi%238", "unit=github/acme%2Fapi%238"},
		{link("://open/"), ""},
		{link("://open/#"), ""},
		{" " + link("://open/#view=inbox&q=a%20b\n"), "view=inbox&q=a%20b"},
	} {
		route, ok := LinkRoute(c.link)
		if !ok || route != c.route {
			t.Errorf("LinkRoute(%q) = (%q, %v), want (%q, true)", c.link, route, ok, c.route)
		}
	}
}

func TestLinkRouteRefusesAMalformedLink(t *testing.T) {
	for _, l := range []string{
		"",
		link("://"),
		link("://other/#task=t-1"),
		link("://open/task#task=t-1"),
		link("://open/?token=a#task=t-1"),
		link("://me@open/#task=t-1"),
		link(":open#task=t-1"),
		"http://open/#task=t-1",
		link("://open/#task=%zz"),
	} {
		if route, ok := LinkRoute(l); ok {
			t.Errorf("LinkRoute(%q) read %q", l, route)
		}
	}
}

func TestIsLinkNamesTheSchemeOnly(t *testing.T) {
	for l, want := range map[string]bool{
		link("://open/#task=t-1"):                        true,
		link("://other"):                                 true,
		strings.ToUpper(brand.URLScheme()) + ":whatever": true,
		"#task=t-1":                                      false,
		"https://example.test/":                          false,
		link("x://open/"):                                false,
	} {
		if got := IsLink(l); got != want {
			t.Errorf("IsLink(%q) = %v, want %v", l, got, want)
		}
	}
}

func TestInsetTitleBarMarksThePageAndKeepsTheRoute(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"http://127.0.0.1:7000/?token=a#task=t-1", "http://127.0.0.1:7000/?token=a&titlebar=inset#task=t-1"},
		{"http://127.0.0.1:7000/?token=a", "http://127.0.0.1:7000/?token=a&titlebar=inset"},
		{"http://127.0.0.1:7000/#view=inbox", "http://127.0.0.1:7000/?titlebar=inset#view=inbox"},
		{"http://127.0.0.1:7000/?#view=inbox", "http://127.0.0.1:7000/?titlebar=inset#view=inbox"},
		{"http://127.0.0.1:7000/?token=a&titlebar=inset#x", "http://127.0.0.1:7000/?token=a&titlebar=inset#x"},
	} {
		if got := InsetTitleBar(c.in); got != c.want {
			t.Errorf("InsetTitleBar(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
