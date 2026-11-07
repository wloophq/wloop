package window

import (
	"net/url"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
)

// MalformedLinkRoute is the route a link that cannot be read opens: the
// default view, which the cockpit tells about the link.
const MalformedLinkRoute = "link=malformed"

const linkHost = "open"

// IsLink says s names the deep link scheme, readable or not.
func IsLink(s string) bool {
	scheme, _, ok := strings.Cut(strings.TrimSpace(s), ":")
	return ok && strings.EqualFold(scheme, brand.URLScheme())
}

// LinkRoute is the cockpit route of a deep link, wloop://open/#<route>; ok is
// false for anything else. The route is kept as written, still escaped.
func LinkRoute(link string) (route string, ok bool) {
	head, route, _ := strings.Cut(strings.TrimSpace(link), "#")
	u, err := url.Parse(head)
	if err != nil || !strings.EqualFold(u.Scheme, brand.URLScheme()) || u.Opaque != "" || u.User != nil ||
		u.Host != linkHost || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery {
		return "", false
	}
	if _, err := url.ParseQuery(route); err != nil {
		return "", false
	}
	return route, true
}

const insetTitleBar = "titlebar=inset"

// InsetTitleBar marks a cockpit URL for a window whose title bar the page
// draws, around the window controls.
func InsetTitleBar(u string) string {
	page, frag, hasFrag := strings.Cut(u, "#")
	_, q, hasQuery := strings.Cut(page, "?")
	if !slices.Contains(strings.Split(q, "&"), insetTitleBar) {
		switch {
		case !hasQuery:
			page += "?"
		case q != "":
			page += "&"
		}
		page += insetTitleBar
	}
	if hasFrag {
		return page + "#" + frag
	}
	return page
}
