package placement

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	RoleReview = "review"
	RoleAuthor = "author"
	RoleWork   = "work"

	CheckoutMain = "main"

	SourceOverride  = "override"
	SourceContext   = "context"
	SourceSuggested = "suggested"
	SourceProvider  = "provider"
)

// Valid says why r is not a usable rule.
func (r Rule) Valid() error {
	if r.Match == "" {
		return errors.New("match is empty")
	}
	if i := strings.IndexAny(r.Match, "[]{}\\\n\r"); i >= 0 {
		return fmt.Errorf("match %q: %q is not a glob character here; use * and ?", r.Match, r.Match[i])
	}
	if r.Checkout != "" && r.Checkout != CheckoutMain && !filepath.IsAbs(r.Checkout) {
		return fmt.Errorf("checkout %q is neither empty, %q nor an absolute path", r.Checkout, CheckoutMain)
	}
	switch r.Role {
	case "", RoleReview, RoleAuthor, RoleWork:
	default:
		return fmt.Errorf("role %q is not %s, %s or %s", r.Role, RoleReview, RoleAuthor, RoleWork)
	}
	return nil
}

// Matches says whether r's glob matches "<provider>:<key>" of u.
func (r Rule) Matches(u unit.Unit) bool { return glob(r.Match, u.String()) }

// Place is the first rule of m that matches u, with Source "context".
func (m Map) Place(u unit.Unit) (Placement, bool) {
	for _, r := range m.Rules {
		if r.Matches(u) {
			return Placement{Repo: r.Repo, Checkout: r.Checkout, Role: r.Role, Shared: r.Shared, Source: SourceContext}, true
		}
	}
	return Placement{}, false
}

func glob(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	pi, ti := 0, 0
	star, mark := -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
