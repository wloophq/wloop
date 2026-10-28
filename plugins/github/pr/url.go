package pr

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	sdk "github.com/wloophq/wloop/pkg/plugin"
)

// URL is the pull request's page on GitHub.
func (r Ref) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", r.Owner, r.Repo, r.Number)
}

var prURL = regexp.MustCompile(`^https?://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)(?:[/?#].*)?$`)

// ParseURL reads a pull request's page address.
func ParseURL(raw string) (Ref, error) {
	m := prURL.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return Ref{}, fmt.Errorf("not a GitHub pull request URL: %s", raw)
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 {
		return Ref{}, fmt.Errorf("not a GitHub pull request URL: %s", raw)
	}
	return Ref{Owner: m[1], Repo: m[2], Number: n}, nil
}

// Unit is the plugin's unit of the reference.
func (r Ref) Unit() sdk.Unit { return sdk.Unit{Provider: Provider, Key: r.String()} }

// RefOf is the pull request u names; false for a unit of another provider
// or a key that names none.
func RefOf(u sdk.Unit) (Ref, bool) {
	if u.Provider != Provider {
		return Ref{}, false
	}
	ref, err := ParseKey(u.Key)
	return ref, err == nil
}
