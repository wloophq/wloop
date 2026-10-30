package pr

import "time"

// The states of a review.
const (
	ReviewApproved         = "APPROVED"
	ReviewChangesRequested = "CHANGES_REQUESTED"
	ReviewCommented        = "COMMENTED"
	ReviewDismissed        = "DISMISSED"
)

// PullRequest is a pull request as a read returned it: the facts the decide
// rules read and the head repository's owner.
type PullRequest struct {
	Facts
	HeadRepositoryOwner string
}

// Facts are a pull request as the decide rules read it.
type Facts struct {
	Ref
	State             string
	IsDraft           bool
	Title             string
	HeadRefName       string
	BaseRefName       string
	HeadRefOid        string
	IsCrossRepository bool
	// Visibility is the repository's visibility, lower-case: public,
	// private or internal; empty when not read.
	Visibility        string
	Author            string
	AuthorAssociation string
	ReviewDecision    string
	// Mergeable is MERGEABLE, CONFLICTING or UNKNOWN (GitHub computes it
	// lazily); empty when not read.
	Mergeable string
	// ChecksState is the status check rollup of the head commit; empty when
	// the repository has no checks.
	ChecksState string
	// NewestChangesRequestOid is the commit the newest CHANGES_REQUESTED
	// review was left on.
	NewestChangesRequestOid string

	Reviews        []Review
	LatestReviews  []Review
	Comments       []Comment
	ReviewRequests []string
	// ViewerRequested says the viewer's review is requested by name.
	ViewerRequested bool
	Commits         []Commit
	// OpenThreads are the unresolved review threads; ThreadsTotal counts
	// every thread, read or not.
	OpenThreads  []Thread
	ThreadsTotal int
	Checks       []Check
	Labels       []string
}

type Review struct {
	ID          string
	State       string
	Author      string
	SubmittedAt time.Time
	CommitOid   string
	// HasContent is set by the author queries only. GitHub requires a body or
	// comments on the states other than APPROVED; an approval counts only
	// with inline comments, as reading every body would weigh the query.
	HasContent bool
	// ByViewer is set by the queries that ask GitHub whether the viewer wrote the review.
	ByViewer bool
}

type Comment struct {
	ID        string
	Author    string
	CreatedAt time.Time
}

// Check is one status check or check run on the head commit.
type Check struct {
	Name    string
	Pending bool
	Failed  bool
	URL     string
	Summary string
}

// Thread is an unresolved review thread, by its last comment.
type Thread struct {
	LastAuthor string
	LastAt     time.Time
}

type Commit struct {
	Oid     string
	Parents int
}
