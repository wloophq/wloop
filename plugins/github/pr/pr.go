// Package pr is the GitHub plugin's pull request: its reference and the
// row the plugin's store keeps of it.
package pr

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Provider is the provider id of the plugin's units.
const Provider = "github"

// StateOpen is an open pull request's state.
const StateOpen = "OPEN"

// ReasonTracked is the scope reason of a pull request the user tracks.
const ReasonTracked = "tracked"

type Ref struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// String is the ref's unit key, owner/repo#number.
func (r Ref) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

// Compare orders refs by owner, repo and number.
func Compare(a, b Ref) int {
	return cmp.Or(cmp.Compare(a.Owner, b.Owner), cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.Number, b.Number))
}

// ParseKey reads a unit key, owner/repo#number.
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

// PR is a pull request as the plugin's store keeps it.
type PR struct {
	Ref            Ref
	Title          string
	Author         string
	State          string // OPEN, MERGED, CLOSED
	IsDraft        bool
	HeadOid        string
	HeadBranch     string
	BaseBranch     string
	IsFork         bool
	ChecksState    string   // SUCCESS, FAILURE, PENDING, "" (no checks)
	ReviewDecision string   // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, "" (no required reviews)
	ReviewRequests []string // current pending reviewers: logins and "team:org/slug"
	URL            string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	MergedAt       time.Time
	ClosedAt       time.Time
	BodyEditedAt   time.Time
	ChangedFiles   int
	LastSeenAt     time.Time // last time a search or a by-ref read returned it
	Reasons        []string  // ownership reasons, sorted; empty means it left scope
	Workspace      Workspace // zero when none
	FailingChecks  []FailingCheck
	Mergeable      string    // MERGEABLE, CONFLICTING, UNKNOWN, "" (not read)
	MergeableAt    time.Time // when it was first seen CONFLICTING; zero otherwise
}

type FailingCheck struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Workspace is the workspace a pull request is linked to.
type Workspace struct {
	// Manager owns the workspace; ID is its id there.
	Manager     string   `json:"manager,omitempty"`
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	IsReview    bool     `json:"is_review"`
	AgentStates []string `json:"agent_states"`
	Comment     string   `json:"comment"`
	// IsMain is the repository's main checkout on the pull request's branch,
	// which the loop never removes.
	IsMain bool `json:"is_main"`
}

// Override is what the user set on a pull request that the plugin keeps.
type Override struct {
	Ref     Ref
	Adopted bool // taken over into the loop
	Tracked bool // added by hand; the store keeps ReasonTracked among its reasons while set
}
