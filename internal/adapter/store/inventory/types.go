package inventory

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
)

// DetailVersion is the shape of Detail this release reads; a stored detail
// of another version is re-read by the next fast run.
const DetailVersion = 2

// Detail is what deciding on a pull request reads beyond its row: the
// collector's full read, stored as GitHub returned it.
type Detail struct {
	Version                 int       `json:"-"`
	ReadAt                  time.Time `json:"-"`
	AuthorAssociation       string
	Visibility              string // the repository's: public, private or internal
	Labels                  []string
	Commits                 []pr.Commit // the newest 50, oldest first
	Reviews                 []pr.Review // the newest 100, oldest first; HasContent set, ByViewer unset
	LatestReviews           []pr.Review // latestReviews(first: 20)
	NewestChangesRequestOid string
	Comments                []pr.Comment // the newest 100, oldest first
	Checks                  []pr.Check   // the head's contexts, first 50
	ThreadsTotal            int
	// OpenThreads holds the unresolved threads among the newest 50 when
	// ThreadsRead: read for a pull request with ThreadsTotal > 0 the viewer
	// authored or develops in a workspace, and known empty when ThreadsTotal
	// is 0.
	OpenThreads []pr.Thread
	ThreadsRead bool
}

// Fresh tells a detail of this release's version read within maxAge of now.
func (d Detail) Fresh(now time.Time, maxAge time.Duration) bool {
	return d.Version == DetailVersion && now.Sub(d.ReadAt) < maxAge
}

// FactsOf are p's facts as the decide rules read them, from the row and its
// detail d. viewer sets ByViewer on reviews and ViewerRequested.
func FactsOf(p dinv.PR, d Detail, viewer string) pr.Facts {
	byViewer := func(login string) bool { return viewer != "" && strings.EqualFold(login, viewer) }
	out := pr.Facts{
		Ref:                     p.Ref,
		State:                   p.State,
		IsDraft:                 p.IsDraft,
		Title:                   p.Title,
		HeadRefName:             p.HeadBranch,
		BaseRefName:             p.BaseBranch,
		HeadRefOid:              p.HeadOid,
		IsCrossRepository:       p.IsFork,
		Visibility:              d.Visibility,
		Author:                  p.Author,
		AuthorAssociation:       d.AuthorAssociation,
		ReviewDecision:          p.ReviewDecision,
		Mergeable:               p.Mergeable,
		ChecksState:             p.ChecksState,
		NewestChangesRequestOid: d.NewestChangesRequestOid,
		LatestReviews:           slices.Clone(d.LatestReviews),
		Comments:                slices.Clone(d.Comments),
		ReviewRequests:          slices.Clone(p.ReviewRequests),
		ViewerRequested:         slices.ContainsFunc(p.ReviewRequests, byViewer),
		Commits:                 slices.Clone(d.Commits),
		OpenThreads:             slices.Clone(d.OpenThreads),
		ThreadsTotal:            d.ThreadsTotal,
		Checks:                  slices.Clone(d.Checks),
		Labels:                  slices.Clone(d.Labels),
	}
	for _, r := range d.Reviews {
		r.Author = bareLogin(r.Author)
		r.ByViewer = byViewer(r.Author)
		out.Reviews = append(out.Reviews, r)
	}
	for i := range out.LatestReviews {
		out.LatestReviews[i].Author = bareLogin(out.LatestReviews[i].Author)
	}
	for i := range out.Comments {
		out.Comments[i].Author = bareLogin(out.Comments[i].Author)
	}
	for i := range out.OpenThreads {
		out.OpenThreads[i].LastAuthor = bareLogin(out.OpenThreads[i].LastAuthor)
	}
	return out
}

// GitHubOf is p as a by-ref read would have returned it to the sweep: its
// FactsOf and, for a same-repository head, the base repository's owner.
func GitHubOf(p dinv.PR, d Detail, viewer string) *pr.PullRequest {
	out := &pr.PullRequest{Facts: FactsOf(p, d, viewer)}
	if !p.IsFork {
		out.HeadRepositoryOwner = p.Ref.Owner
	}
	return out
}

// bareLogin drops the "[bot]" the full read gives a bot author: the sweep's
// reviews, comments and threads name it by its bare login.
func bareLogin(login string) string { return strings.TrimSuffix(login, "[bot]") }

const (
	outcomeRunning     = "running"
	outcomeOK          = "ok"
	outcomeReused      = "reused"
	outcomeFailed      = "failed"
	outcomeInterrupted = "interrupted"
)

// StartBlock is a round blocked until the agent home answers Question for
// Root, the repository's main checkout, or for Folder, the removed worktree.
type StartBlock struct {
	Question, Root, Folder string
	// Home is the agent home the session ran with, "" for its terminal's own.
	Home string
	// Homes are the agent homes trusting the folder writes in: Home, or
	// the homes the terminal's own one may be.
	Homes []string
	// Round names the round the block holds; Skip are the marks dismissing
	// the block writes, its plan's OnSkipped.
	Round string
	Skip  map[string]string
}

// Key keeps each block its own event: a keyed event is recorded once. Its
// eighth and ninth parts, the reviews and checks head an older release
// wrote, stay empty, and the tenth is Skip as JSON when it holds a mark.
func (b StartBlock) Key(at time.Time) string {
	parts := []string{at.UTC().Format(time.RFC3339Nano), b.Question, b.Root, b.Folder, b.Home, strings.Join(b.Homes, "\t"), b.Round, "", ""}
	if len(b.Skip) > 0 {
		skip, _ := json.Marshal(b.Skip)
		parts = append(parts, string(skip))
	}
	return strings.Join(parts, "\n")
}

// StartBlockOf is the StartBlock a round_blocked event's key names.
func StartBlockOf(e port.Event) (StartBlock, bool) {
	parts := strings.Split(e.Key, "\n")
	if e.Kind != dinv.KindRoundBlocked || (len(parts) != 9 && len(parts) != 10) {
		return StartBlock{}, false
	}
	b := StartBlock{Question: parts[1], Root: parts[2], Folder: parts[3], Home: parts[4], Round: parts[6]}
	if parts[5] != "" {
		b.Homes = strings.Split(parts[5], "\t")
	}
	if len(parts) == 10 && strings.HasPrefix(parts[9], "{") {
		if err := json.Unmarshal([]byte(parts[9]), &b.Skip); err != nil {
			return StartBlock{}, false
		}
		return b, true
	}
	conflict := ""
	if len(parts) == 10 {
		conflict = parts[9]
	}
	b.Skip = olderSkip(b.Round, parts[7], parts[8], conflict)
	return b, true
}

// olderSkip are the marks dismissing a block an older release keyed writes:
// it named the reviews and heads the GitHub round answered, not its marks,
// which are the GitHub planner's mark names of that release.
func olderSkip(kind, reviews, checks, conflict string) map[string]string {
	switch kind {
	case "first round":
		return map[string]string{"first.skipped": "1"}
	case "triage":
		out := map[string]string{}
		for name, v := range map[string]string{"addressed.reviews": reviews, "addressed.checks": checks, "addressed.conflict": conflict} {
			if v != "" {
				out[name] = v
			}
		}
		return out
	}
	return map[string]string{"reviewed": reviews}
}
