package brief

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// PRReader reads a pull request the inventory holds.
type PRReader interface {
	PR(ctx context.Context, ref github.Ref) (dinv.PR, bool, error)
}

// GitHubSource is the BriefSource of pull requests: the revision is the
// head and the description's edit time, the parts the changed files; its
// staleness and attempt rules are the queue's.
type GitHubSource struct {
	Store  PRReader
	Source interface {
		Facts(ctx context.Context, ref github.Ref) (Input, error)
		Diff(ctx context.Context, ref github.Ref, limit int) (string, bool, error)
	}
	MaxDiffBytes int
	// Retry is the first wait after a failed attempt, the queue's Deps.Retry.
	Retry time.Duration
}

// prParts are a pull request's parts of its input, as brief_runs.input
// stores them.
type prParts struct {
	ChangedFiles int      `json:"changed_files"`
	Files        []string `json:"files,omitempty"`
}

func (g GitHubSource) ref(u unit.Unit) (github.Ref, error) {
	if u.Provider != unit.GitHub {
		return github.Ref{}, fmt.Errorf("brief: %s is not a pull request", u)
	}
	return pr.ParseKey(u.Key)
}

// AtOf is what p is at, in the format brief_runs stores.
func AtOf(p dinv.PR) At {
	parts, _ := json.Marshal(prParts{ChangedFiles: p.ChangedFiles})
	return At{Revision: p.HeadOid + " " + timeText(p.BodyEditedAt), Parts: parts}
}

func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

// prAt reads back the pull request fields At holds.
func prAt(a At) (dinv.PR, dinv.BriefRecord, error) {
	head, edited, _ := strings.Cut(a.Revision, " ")
	var p dinv.PR
	p.HeadOid = head
	if edited != "" {
		t, err := time.Parse("2006-01-02T15:04:05.000000000Z", edited)
		if err != nil {
			return p, dinv.BriefRecord{}, fmt.Errorf("brief: revision %q: %w", a.Revision, err)
		}
		p.BodyEditedAt = t
	}
	var parts prParts
	if len(a.Parts) > 0 {
		if err := json.Unmarshal(a.Parts, &parts); err != nil {
			return p, dinv.BriefRecord{}, fmt.Errorf("brief: parts %s: %w", a.Parts, err)
		}
	}
	p.ChangedFiles = parts.ChangedFiles
	return p, dinv.BriefRecord{HeadOid: p.HeadOid, BodyEditedAt: p.BodyEditedAt, ChangedFiles: p.ChangedFiles}, nil
}

func (g GitHubSource) Revision(ctx context.Context, u unit.Unit) (At, error) {
	ref, err := g.ref(u)
	if err != nil {
		return At{}, err
	}
	p, ok, err := g.Store.PR(ctx, ref)
	if err != nil || !ok {
		return At{}, err
	}
	return AtOf(p), nil
}

func (g GitHubSource) Input(ctx context.Context, u unit.Unit) (UnitInput, error) {
	ref, err := g.ref(u)
	if err != nil {
		return UnitInput{}, err
	}
	in, err := g.Source.Facts(ctx, ref)
	if err != nil {
		return UnitInput{}, err
	}
	diff, cut, err := g.Source.Diff(ctx, ref, g.MaxDiffBytes)
	if err != nil {
		return UnitInput{}, err
	}
	parts, _ := json.Marshal(prParts{ChangedFiles: len(in.Paths), Files: in.Paths})
	return UnitInput{Text: diff, Bytes: len(diff), Cut: cut, Parts: parts}, nil
}

func (g GitHubSource) Describe(in UnitInput) string {
	var parts prParts
	_ = json.Unmarshal(in.Parts, &parts)
	cut := ""
	if in.Cut {
		cut = ", cut"
	}
	return fmt.Sprintf("a pull request of %d changed files, its diff %d bytes%s", parts.ChangedFiles, in.Bytes, cut)
}

// Stale is the queue's rule: a failed attempt is due when the pull request
// moved or its backoff passed, a brief when the pull request moved.
func (g GitHubSource) Stale(w Written, cur At, version string, now time.Time) bool {
	p, _, err := prAt(cur)
	if err != nil {
		return true
	}
	_, rec, err := prAt(w.At)
	if err != nil {
		return true
	}
	rec.Version, rec.Error, rec.Failures, rec.AttemptedAt, rec.AttemptOf = w.Version, w.Error, w.Failures, w.AttemptedAt, w.AttemptOf
	if rec.Error != "" {
		return rec.AttemptOf != attemptOf(p, version) || now.Sub(rec.AttemptedAt) >= backoff(g.Retry, rec.Failures)
	}
	return moved(p, rec, now)
}

func (g GitHubSource) AttemptKey(version string, cur At) string {
	p, _, err := prAt(cur)
	if err != nil {
		return version + " " + cur.Revision
	}
	return attemptOf(p, version)
}
