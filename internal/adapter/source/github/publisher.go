package ghsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

// mayHaveBeenPosted starts a Publish error after the request may have
// reached GitHub; the outbox asks for a confirmation before a retry.
const mayHaveBeenPosted = "may have been posted"

// verifyPages bounds how many pages of comments Verify reads.
const verifyPages = 10

// clockSkew widens Verify's window: GitHub's clock stamps created_at.
const clockSkew = time.Minute

type restClient interface {
	REST(ctx context.Context, r github.Request) (github.Response, error)
}

type eventRecorder interface {
	RecordEvent(ctx context.Context, ev dinv.Event) error
}

// Publisher posts GitHub drafts as issue comments on their pull request.
type Publisher struct {
	GH     restClient
	Viewer func(ctx context.Context) (string, error)
	Events eventRecorder
	// People are the persons the unit's items name, for mentions of other providers.
	People func(ctx context.Context, u unit.Unit) ([]items.Person, error)
	Now    func() time.Time
	Log    func(format string, args ...any)
}

var (
	_ port.Effects   = (*Publisher)(nil)
	_ port.Validator = (*Publisher)(nil)
	_ port.Locator   = (*Publisher)(nil)
)

type issueComment struct {
	ID        int64     `json:"id"`
	HTMLURL   string    `json:"html_url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Validate refuses a draft GitHub cannot take as a comment on the draft's own pull request.
func (p *Publisher) Validate(ctx context.Context, d draft.Draft) error {
	_, _, err := p.prepare(ctx, d)
	return err
}

// Where names the pull request d comments on.
func (p *Publisher) Where(ctx context.Context, d draft.Draft) (string, error) {
	ref, _, err := p.prepare(ctx, d)
	if err != nil {
		return "", err
	}
	return ref.String(), nil
}

func (p *Publisher) prepare(ctx context.Context, d draft.Draft) (pr.Ref, string, error) {
	if d.Unit.Provider != unit.GitHub {
		return pr.Ref{}, "", fmt.Errorf("github publishes only github units, not %s", d.Unit.Label())
	}
	if d.Delivery == draft.Reaction {
		return pr.Ref{}, "", fmt.Errorf("github cannot publish a %s draft", d.Delivery)
	}
	ref, err := pr.ParseKey(d.Unit.Key)
	if err != nil {
		return pr.Ref{}, "", err
	}
	if d.Target != "" {
		t, err := pr.ParseKey(d.Target)
		if err != nil {
			return pr.Ref{}, "", fmt.Errorf("target %q is not owner/repo#n", d.Target)
		}
		if !strings.EqualFold(t.String(), ref.String()) {
			return pr.Ref{}, "", fmt.Errorf("target %s is not the draft's pull request %s", t, ref)
		}
	}
	body, err := p.body(ctx, d)
	if err != nil {
		return pr.Ref{}, "", err
	}
	if strings.TrimSpace(body) == "" {
		return pr.Ref{}, "", errors.New("the comment is empty")
	}
	return ref, body, nil
}

var mentionRE = regexp.MustCompile(`@\{([^{}\s]+)\}`)

// body is d's text as GitHub takes it: a github mention as @login, any
// other person by name.
func (p *Publisher) body(ctx context.Context, d draft.Draft) (string, error) {
	keys := mentionRE.FindAllStringSubmatch(d.Text, -1)
	if len(keys) == 0 {
		return d.Text, nil
	}
	var names map[string]string
	var errs []error
	out := mentionRE.ReplaceAllStringFunc(d.Text, func(m string) string {
		key := m[2 : len(m)-1]
		provider, id, ok := strings.Cut(key, ":")
		switch {
		case !ok || id == "":
			errs = append(errs, fmt.Errorf("mention %s is not @{<provider>:<id>}", m))
			return m
		case provider == string(unit.GitHub):
			return "@" + id
		}
		if names == nil {
			var err error
			if names, err = p.names(ctx, d.Unit); err != nil {
				errs = append(errs, err)
				return m
			}
		}
		if n, ok := names[key]; ok {
			return unmention(n)
		}
		if provider == "group" {
			return unmention(id)
		}
		errs = append(errs, fmt.Errorf("mention %s names nobody the pull request's items know", m))
		return m
	})
	return out, errors.Join(errs...)
}

// unmention keeps a name from another provider from pinging whoever GitHub knows by an @ inside it.
func unmention(name string) string {
	return strings.ReplaceAll(name, "@", "@\u200b")
}

func (p *Publisher) names(ctx context.Context, u unit.Unit) (map[string]string, error) {
	out := map[string]string{}
	if p.People == nil {
		return out, nil
	}
	people, err := p.People(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("reading the persons of %s: %w", u.Label(), err)
	}
	for _, x := range people {
		if x.Name == "" {
			continue
		}
		for _, k := range append([]string{x.Key}, x.Aliases...) {
			if _, ok := out[k]; !ok {
				out[k] = x.Name
			}
		}
	}
	return out, nil
}

// Publish posts d as a comment. A nudge then records its nudged event, as
// the nudge intent does, so the comment reads as a nudge; a refused post
// records none.
func (p *Publisher) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	ref, body, err := p.prepare(ctx, d)
	if err != nil {
		return draft.Evidence{}, err
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return draft.Evidence{}, err
	}
	before := p.Now()
	resp, err := p.GH.REST(ctx, github.Request{Method: http.MethodPost, Path: commentsPath(ref), Body: payload, Mutation: true})
	if err != nil {
		if written(err) {
			p.recordNudge(ctx, d, ref, body, before)
			return draft.Evidence{}, fmt.Errorf("%s on %s: %w", mayHaveBeenPosted, ref, err)
		}
		return draft.Evidence{}, fmt.Errorf("commenting on %s: %w", ref, err)
	}
	var c issueComment
	if err := json.Unmarshal(resp.Body, &c); err != nil || c.ID == 0 {
		p.recordNudge(ctx, d, ref, body, before)
		return draft.Evidence{}, fmt.Errorf("%s on %s: GitHub's answer names no comment", mayHaveBeenPosted, ref)
	}
	p.recordNudge(ctx, d, ref, body, c.CreatedAt)
	return evidenceOf(c), nil
}

// recordNudge applies a draft's pr.nudged as it posts: the nudged event at
// at; a failure is only logged, since failing a posted draft would invite a
// second post.
func (p *Publisher) recordNudge(ctx context.Context, d draft.Draft, ref pr.Ref, body string, at time.Time) {
	if !slices.Contains(d.Effects, builtin.EffectPRNudged) {
		return
	}
	if at.IsZero() {
		at = p.Now()
	}
	if err := p.Events.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindNudged, At: at, Data: nudged(body)}); err != nil && p.Log != nil {
		p.Log("github: recording the nudge of draft %d on %s: %v", d.ID, ref, err)
	}
}

// Verify looks for the viewer's comment with d's body among those since startedAt.
func (p *Publisher) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	ref, body, err := p.prepare(ctx, d)
	if err != nil {
		return draft.Evidence{}, false, err
	}
	viewer, err := p.Viewer(ctx)
	if err != nil {
		return draft.Evidence{}, false, err
	}
	if viewer == "" {
		return draft.Evidence{}, false, errors.New("github: no viewer stored yet")
	}
	from := startedAt.Add(-clockSkew)
	q := url.Values{"since": {from.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	for page := 1; page <= verifyPages; page++ {
		q.Set("page", strconv.Itoa(page))
		resp, err := p.GH.REST(ctx, github.Request{Path: commentsPath(ref) + "?" + q.Encode()})
		if err != nil {
			return draft.Evidence{}, false, fmt.Errorf("listing the comments of %s: %w", ref, err)
		}
		var cs []issueComment
		if err := json.Unmarshal(resp.Body, &cs); err != nil {
			return draft.Evidence{}, false, fmt.Errorf("listing the comments of %s: %w", ref, err)
		}
		for _, c := range cs {
			if strings.EqualFold(c.User.Login, viewer) && !c.CreatedAt.Before(from) && sameBody(c.Body, body) {
				p.recordNudge(ctx, d, ref, body, c.CreatedAt)
				return evidenceOf(c), true, nil
			}
		}
		if len(cs) < 100 {
			break
		}
	}
	return draft.Evidence{}, false, nil
}

func commentsPath(ref pr.Ref) string {
	return fmt.Sprintf("repos/%s/%s/issues/%d/comments", url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), ref.Number)
}

func evidenceOf(c issueComment) draft.Evidence {
	return draft.Evidence{Ref: strconv.FormatInt(c.ID, 10), Permalink: c.HTMLURL, At: c.CreatedAt}
}

func sameBody(a, b string) bool {
	norm := func(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }
	return norm(a) == norm(b)
}

// written says whether a failed mutation may have reached GitHub: anything
// but an answer GitHub refused it with, or a failure before the request left.
func written(err error) bool {
	var se *github.StatusError
	var rl *github.RateLimitedError
	var be *dgh.BudgetError
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.As(err, &se):
		return se.Status >= 500
	case errors.As(err, &rl), errors.As(err, &be), errors.Is(err, github.ErrUnauthorized), errors.As(err, &dns):
		return false
	case errors.As(err, &op):
		return op.Op != "dial" && op.Op != "proxyconnect"
	}
	return true
}

var loginMentionRE = regexp.MustCompile("(?:^|[^\\w`])@([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:/[A-Za-z0-9._-]+)?)")

// nudged is whom body asks, as the nudge intent records it.
func nudged(body string) string {
	var who []string
	for _, m := range loginMentionRE.FindAllStringSubmatch(body, -1) {
		if w := "@" + m[1]; !slices.ContainsFunc(who, func(s string) bool { return strings.EqualFold(s, w) }) {
			who = append(who, w)
		}
	}
	if len(who) == 0 {
		return prrow.NudgeMaintainers
	}
	return strings.Join(who, ", ")
}

// publisherOf is the module's publisher, nil without a GitHub client.
func publisherOf(d sourcereg.Deps) port.Effects {
	if d.GitHub == nil || d.Inventory == nil {
		return nil
	}
	p := &Publisher{
		GH:     d.GitHub,
		Viewer: func(ctx context.Context) (string, error) { return d.Inventory.Viewer(ctx, unit.GitHub) },
		Events: d.Inventory,
		Now:    d.Clock.Now,
		Log:    d.Log,
	}
	if d.DB != nil {
		st := d.DB.Items()
		p.People = func(ctx context.Context, u unit.Unit) ([]items.Person, error) {
			byUnit, err := st.Load(ctx, []unit.Unit{u})
			if err != nil {
				return nil, err
			}
			var out []items.Person
			for _, it := range byUnit[u] {
				out = append(append(append(out, it.Requester), it.Assignees...), it.WaitingOn...)
			}
			return out, nil
		}
	}
	return p
}
