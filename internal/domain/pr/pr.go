// Package pr names a GitHub pull request.
package pr

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

type Ref struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

func (r Ref) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

// RemoteKey is the repository's key among a workspace manager's repositories.
func (r Ref) RemoteKey() string { return remoteHost + r.Owner + "/" + r.Repo }

const remoteHost = "github.com/"

func (r Ref) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", r.Owner, r.Repo, r.Number)
}

var prURL = regexp.MustCompile(`^https://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)(?:[/?#].*)?$`)

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

// ParseKey parses the key of a github unit, "owner/repo#12".
func ParseKey(key string) (Ref, error) {
	bad := fmt.Errorf("not a pull request key: %q", key)
	slash := strings.Index(key, "/")
	hash := strings.LastIndex(key, "#")
	if slash <= 0 || hash <= slash+1 {
		return Ref{}, bad
	}
	digits := key[hash+1:]
	n, err := strconv.Atoi(digits)
	if err != nil || digits[0] < '0' || digits[0] > '9' {
		return Ref{}, bad
	}
	ref := Ref{Owner: key[:slash], Repo: key[slash+1 : hash], Number: n}
	if ref.String() != key {
		return Ref{}, bad
	}
	return ref, nil
}

// Unit is the github unit of the reference; the zero reference is the zero unit.
func (r Ref) Unit() unit.Unit {
	if r == (Ref{}) {
		return unit.Unit{}
	}
	return unit.Unit{Provider: unit.GitHub, Key: r.String()}
}

// Codec is the unit codec of provider "github".
type Codec struct{}

func (Codec) Provider() unit.ProviderID { return unit.GitHub }

// Valid refuses a key that is no pull request: number 0 names none.
func (Codec) Valid(key string) error {
	ref, err := ParseKey(key)
	if err == nil && ref.Number <= 0 {
		return fmt.Errorf("not a pull request key: %q", key)
	}
	return err
}

// URL is the pull request's address; a key that is not one names none.
func (Codec) URL(key string) string {
	ref, err := ParseKey(key)
	if err != nil {
		return ""
	}
	return ref.URL()
}

// KeyOf is the key of the pull request a URL names.
func (Codec) KeyOf(raw string) (string, bool) {
	ref, err := ParseURL(raw)
	if err != nil {
		return "", false
	}
	return ref.String(), true
}

// Equal folds case, as GitHub compares owners, repositories and logins.
func (Codec) Equal(a, b string) bool { return strings.EqualFold(a, b) }
