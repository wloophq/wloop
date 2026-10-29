package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
)

var _ src.Briefer = (*Plugin)(nil)

// The brief rules of a pull request, as the host's queue keeps them.
const (
	// refreshFiles is how many files a new head must add before the brief
	// is made again.
	refreshFiles = 5
	// bodyQuiet is how long an edited description must sit unchanged
	// before it is briefed again.
	bodyQuiet = 10 * time.Minute
	// maxBackoff caps the wait of a pull request that keeps failing.
	maxBackoff = 24 * time.Hour
	bodyMax    = 8000
	issueMax   = 4000
)

// BriefParts are a pull request's parts of its brief input: what
// brief_runs.input stores, and the facts the prompt lays out besides the
// diff, which is the input's text.
type BriefParts struct {
	ChangedFiles int      `json:"changed_files"`
	Files        []string `json:"files,omitempty"`
	Title        string   `json:"title,omitempty"`
	Body         string   `json:"body,omitempty"`
	Issue        string   `json:"issue,omitempty"`
	// Listed are the changed files as the prompt lists them, with their
	// additions and deletions.
	Listed []string `json:"listed,omitempty"`
	// DiffError is why GitHub served no diff; "" with one.
	DiffError string `json:"diff_error,omitempty"`
}

// BriefInput reads what a brief of a pull request reads now: its title,
// description, first closing issue and changed files, then its diff, under
// the priority the host's call carries.
func (p *Plugin) BriefInput(ctx context.Context, u sdk.Unit) (src.BriefInput, error) {
	r, err := p.running()
	if err != nil {
		return src.BriefInput{}, err
	}
	ref, err := refOf(u)
	if err != nil {
		return src.BriefInput{}, err
	}
	prio := api.PriorityUser
	if src.CallOf(ctx).Priority == src.PriorityBackground {
		prio = api.PriorityBackground
	}
	r.cfgMu.Lock()
	limit := r.cfg.MaxDiffBytes
	r.cfgMu.Unlock()
	return briefInput(api.WithPriority(ctx, prio), r.client, ref, limit)
}

// briefClient is what a brief's input reads of GitHub.
type briefClient interface {
	BriefFacts(ctx context.Context, ref api.Ref) (api.BriefFacts, error)
	Diff(ctx context.Context, ref api.Ref, limit int) ([]byte, bool, error)
}

// briefInput reads ref's facts, then its diff of at most limit bytes; a diff
// GitHub does not serve is noted in the parts. A refused token or a low
// budget is the host's to wait out.
func briefInput(ctx context.Context, c briefClient, ref pr.Ref, limit int) (src.BriefInput, error) {
	f, err := c.BriefFacts(ctx, ref)
	if err != nil {
		return src.BriefInput{}, unavailable(err)
	}
	parts := BriefParts{Title: f.Title, Body: cut(f.Body, bodyMax)}
	for _, file := range f.Files {
		parts.Listed = append(parts.Listed, fmt.Sprintf("%s (+%d -%d)", file.Path, file.Additions, file.Deletions))
		parts.Files = append(parts.Files, file.Path)
	}
	if f.MoreFiles > 0 {
		parts.Listed = append(parts.Listed, fmt.Sprintf("and %d more files", f.MoreFiles))
	}
	if f.Issue != nil {
		parts.Issue = f.Issue.Title + "\n\n" + cut(f.Issue.Body, issueMax)
	}
	parts.ChangedFiles = len(parts.Files)
	diff, truncated, err := c.Diff(ctx, ref, limit)
	switch {
	case errors.Is(err, api.ErrBudget), errors.Is(err, api.ErrUnauthorized):
		return src.BriefInput{}, unavailable(err)
	case err != nil:
		parts.DiffError, diff, truncated = err.Error(), nil, false
	case truncated:
		diff = wholeRunes(diff)
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return src.BriefInput{}, err
	}
	return src.BriefInput{Text: string(diff), Bytes: int64(len(diff)), Cut: truncated, Parts: raw}, nil
}

// unavailable is err, a refused token or a low budget, as the host waits
// it out.
func unavailable(err error) error {
	if errors.Is(err, api.ErrBudget) || errors.Is(err, api.ErrUnauthorized) {
		return fmt.Errorf("%w: %w", src.ErrUnavailable, err)
	}
	return err
}

// BriefRevision is what the stored pull request is at: its head and its
// description's edit time, its changed files, and the attempt key the
// brief runs have stored since version 4.
func (p *Plugin) BriefRevision(ctx context.Context, u sdk.Unit) (src.BriefRevision, error) {
	r, err := p.running()
	if err != nil {
		return src.BriefRevision{}, err
	}
	ref, err := refOf(u)
	if err != nil {
		return src.BriefRevision{}, err
	}
	row, ok, err := r.db.PR(ctx, ref)
	if err != nil || !ok {
		return src.BriefRevision{}, err
	}
	return revisionOf(row), nil
}

func revisionOf(p pr.PR) src.BriefRevision {
	parts, _ := json.Marshal(BriefParts{ChangedFiles: p.ChangedFiles})
	return src.BriefRevision{Revision: p.HeadOid + " " + timeText(p.BodyEditedAt), Parts: parts,
		Attempt: fmt.Sprintf("%s %s %d", p.HeadOid, p.BodyEditedAt.UTC().Format(time.RFC3339), p.ChangedFiles)}
}

func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

// revisionPR reads back the pull request fields a revision holds.
func revisionPR(rev src.BriefRevision) (pr.PR, error) {
	head, edited, _ := strings.Cut(rev.Revision, " ")
	p := pr.PR{HeadOid: head}
	if edited != "" {
		t, err := time.Parse("2006-01-02T15:04:05.000000000Z", edited)
		if err != nil {
			return p, fmt.Errorf("revision %q: %w", rev.Revision, err)
		}
		p.BodyEditedAt = t
	}
	var parts BriefParts
	if len(rev.Parts) > 0 {
		if err := json.Unmarshal(rev.Parts, &parts); err != nil {
			return p, fmt.Errorf("parts %s: %w", rev.Parts, err)
		}
	}
	p.ChangedFiles = parts.ChangedFiles
	return p, nil
}

// BriefDescribe is in, one line, as the prompt's header names it.
func (p *Plugin) BriefDescribe(ctx context.Context, in src.BriefInput) (string, error) {
	r, err := p.running()
	if err != nil {
		return "", err
	}
	w, err := r.words(ctx)
	if err != nil {
		return "", err
	}
	if w.BriefInput == "" {
		return "", fmt.Errorf("the profile layer's %s names no brief_input", wordingFile)
	}
	t, err := template.New("brief_input").Option("missingkey=error").Parse(w.BriefInput)
	if err != nil {
		return "", fmt.Errorf("the profile layer's %s brief_input: %w", wordingFile, err)
	}
	var parts BriefParts
	_ = json.Unmarshal(in.Parts, &parts)
	var b strings.Builder
	err = t.Execute(&b, struct {
		ChangedFiles int
		Bytes        int64
		Cut          bool
	}{parts.ChangedFiles, in.Bytes, in.Cut})
	return b.String(), err
}

// BriefStale is the queue's rule: a failed attempt is due when the pull
// request moved or its backoff passed, a brief when the pull request moved:
// its description edited a quiet while ago, or a head that added more than
// a few files.
func (p *Plugin) BriefStale(_ context.Context, req src.BriefStaleRequest) (bool, error) {
	cur, err := revisionPR(req.Current)
	if err != nil {
		return true, nil
	}
	was, err := revisionPR(req.Written.At)
	if err != nil {
		return true, nil
	}
	w := req.Written
	if w.Error != "" {
		return w.AttemptOf != req.Version+" "+req.Current.Attempt || req.Now.Sub(w.AttemptedAt) >= backoff(p.retry(), w.Failures), nil
	}
	files := was.ChangedFiles > 0 && cur.ChangedFiles-was.ChangedFiles > refreshFiles
	body := cur.BodyEditedAt.After(was.BodyEditedAt) && !req.Now.Before(cur.BodyEditedAt.Add(bodyQuiet))
	return files || body, nil
}

// BriefWant is false: the host's queue writes a pull request's brief, not
// the plugin.
func (p *Plugin) BriefWant(context.Context, sdk.Unit) (bool, error) { return false, nil }

// retry is the first wait after a failed attempt, the slow sweep's
// interval.
func (p *Plugin) retry() time.Duration {
	r, err := p.running()
	if err != nil {
		return defaultSlow
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.cfg.Collect.Slow
}

// backoff doubles retry after each consecutive failure, up to maxBackoff.
func backoff(retry time.Duration, failures int) time.Duration {
	d := retry
	for i := 1; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, max(retry, maxBackoff))
}

// wholeRunes drops a rune the read cut in two at the end of b.
func wholeRunes(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}

// cut keeps at most n bytes of s, ending on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
