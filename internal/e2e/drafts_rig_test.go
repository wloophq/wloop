package e2e

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/cli"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	ghplugin "github.com/wloophq/wloop/plugins/github"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
)

// ghPublisher is the GitHub publisher of the drafts scenarios: it posts into a list, refuses a draft whose basis
// is not the live head as the real one re-reads it, reads its head as the live basis, and fails the next post
// with fail.
type ghPublisher struct {
	mu       sync.Mutex
	head     string
	posts    []draft.Draft
	failNext error
}

func (p *ghPublisher) Delivers(k draft.Kind) draft.Delivery {
	switch k {
	case "review":
		return "review"
	case "thread_reply":
		return "reply"
	case "resolve":
		return "resolve"
	}
	return draft.Delivery("comment")
}

func (p *ghPublisher) Publish(_ context.Context, d draft.Draft) (draft.Evidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d.Basis != "" && d.Basis != p.head {
		return draft.Evidence{}, fmt.Errorf("%w: the head is %s, the draft read %s", outbox.ErrStale, p.head, d.Basis)
	}
	if err := p.failNext; err != nil {
		p.failNext = nil
		return draft.Evidence{}, err
	}
	p.posts = append(p.posts, d)
	return draft.Evidence{Ref: strconv.FormatInt(int64(d.ID), 10),
		Permalink: fmt.Sprintf("https://github.com/acme/api/pull/7#pullrequestreview-%d", 9000+d.ID)}, nil
}

func (p *ghPublisher) Live(context.Context, unit.Unit, string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.head, nil
}

func (p *ghPublisher) Verify(context.Context, draft.Draft, time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

func (p *ghPublisher) push(head string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.head = head
}

func (p *ghPublisher) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext = err
}

// posted are the drafts posted, "<id> <event>" for a review and "<id> <kind>" for any other.
func (p *ghPublisher) posted() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, d := range p.posts {
		var pl struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal(d.Payload, &pl)
		out = append(out, fmt.Sprintf("%d %s", d.ID, cmp.Or(pl.Event, string(d.Kind))))
	}
	return out
}

var draftsPR = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}

const draftsURL = "https://github.com/acme/api/pull/7"

// ghAPI is the GitHub the real plugin reads in the scenarios: pull request acme/api#7, open at head c2, and
// the viewer; any write, or any other read, fails.
type ghAPI struct{}

const draftsBasics = `{"data":{"pr":{"pullRequest":{"number":7,"title":"Add x","state":"OPEN","headRefName":"feat/x","headRefOid":"c2","repository":{"nameWithOwner":"acme/api"}}}}}`

func (ghAPI) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		return nil, nil
	}
	if len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
		q := args[len(args)-1]
		switch {
		case strings.Contains(q, "mutation"):
		case strings.Contains(q, "pr: repository"):
			return []byte(draftsBasics), nil
		case strings.Contains(q, "viewer"):
			return []byte(`{"data":{"viewer":{"login":"me"}}}`), nil
		}
	}
	return nil, fmt.Errorf("the scenarios' GitHub does not answer gh %s", strings.Join(args, " "))
}

// draftsRig is the scripted agent and the person of the scenarios, over the daemon serve composes, with the
// real GitHub plugin over ghAPI and the GitHub module's publisher a ghPublisher.
type draftsRig struct {
	t   *testing.T
	ctx context.Context
	d   *cli.TestDaemon
	c   *client.Client
	pub *ghPublisher
	s   cli.TestSession
}

var draftsNow = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

func newDraftsRig(t *testing.T) *draftsRig {
	t.Helper()
	now := func() time.Time { return draftsNow }
	r := &draftsRig{t: t, ctx: t.Context(), pub: &ghPublisher{head: "c2"}}
	r.d = cli.ServeForTest(t, cli.TestOptions{Now: now, Publishers: map[unit.ProviderID]port.Effects{unit.GitHub: r.pub},
		Plugins: map[string]src.Source{string(unit.GitHub): &ghplugin.Plugin{API: apitest.Options(ghAPI{}), GH: ghAPI{}, Now: now}}})
	r.c = r.d.Client()
	return r
}

func (r *draftsRig) review() { r.s = r.d.Start(r.t, draftsPR, "review") }

// resumed follows the session to the pull request's newest turn, a resume or a round a workflow reopened.
func (r *draftsRig) resumed() {
	r.t.Helper()
	b := r.book()
	if len(b.Turns) == 0 {
		r.t.Fatal("no turn")
	}
	r.s.Round = b.Turns[len(b.Turns)-1].ID
}

// instructions are the stored instructions of the session's turn, as the agent reads them.
func (r *draftsRig) instructions() string {
	r.t.Helper()
	b := r.book()
	got, err := r.c.StartTurn(r.ctx, r.s.Handle, api.TurnStartRequest{Turn: r.s.Round, Intent: string(b.Turns[len(b.Turns)-1].Intent)})
	if err != nil {
		r.t.Fatalf("instructions of %s: %v", r.s.Round, err)
	}
	return got.Text
}

// propose is the scripted agent proposing a review with event, read at basis.
func (r *draftsRig) propose(event, text, basis string, hold bool) api.AgentDraft {
	r.t.Helper()
	d, err := r.c.ProposeDraft(r.ctx, r.s.Handle, api.AgentDraftProposal{Kind: "review", Text: text,
		Payload: json.RawMessage(fmt.Sprintf(`{"event":%q}`, event)), Basis: basis, Hold: hold})
	if err != nil {
		r.t.Fatalf("propose %s: %v", event, err)
	}
	return d
}

func (r *draftsRig) update(u api.AgentDraftUpdate) (api.AgentDraft, error) {
	return r.c.UpdateDraft(r.ctx, r.s.Handle, u)
}

func (r *draftsRig) end(raw string) {
	r.d.EndTurn(r.t, r.s, api.TurnEndRequest{Turn: r.s.Round, PR: draftsURL, Record: json.RawMessage(raw)})
}

func (r *draftsRig) send() {
	r.t.Helper()
	if err := r.d.Send(r.ctx); err != nil {
		r.t.Fatalf("send: %v", err)
	}
}

func (r *draftsRig) draft(id int64) api.Draft {
	r.t.Helper()
	d, err := r.c.Draft(r.ctx, id)
	if err != nil {
		r.t.Fatalf("draft %d: %v", id, err)
	}
	return d
}

// approve is the person approving draft id at rev on its card.
func (r *draftsRig) approve(id, rev int64) error {
	r.t.Helper()
	_, err := r.c.ResolveGate(r.ctx, r.draft(id).Gate, "approve", api.TaskRequest{Args: map[string]string{"rev": strconv.FormatInt(rev, 10)}, Source: "cli"})
	return err
}

// editPayload is the person changing draft id's payload on its card, at rev.
func (r *draftsRig) editPayload(id, rev int64, payload string) error {
	_, err := r.c.TaskDo(r.ctx, "draft:"+strconv.FormatInt(id, 10), "edit",
		api.TaskRequest{Args: map[string]string{"rev": strconv.FormatInt(rev, 10), "payload": payload}, Source: "cli"})
	return err
}

// answer is the person answering, from the cockpit, the question the pull request's turn asks once the daemon
// has opened its gate: an option's id picks it.
func (r *draftsRig) answer(text string) {
	r.t.Helper()
	var gs []api.GateView
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var err error
		if gs, err = r.c.Gates(r.ctx); err != nil {
			r.t.Fatal(err)
		}
		for _, g := range gs {
			if g.Kind != "question" || g.Unit != draftsPR.String() {
				continue
			}
			for _, in := range g.Intents {
				if in.Answer {
					if _, err := r.c.ResolveGate(r.ctx, g.ID, in.ID, api.TaskRequest{Args: map[string]string{"answer": text}, Source: "cockpit"}); err != nil {
						r.t.Fatalf("answering %q: %v", text, err)
					}
					return
				}
			}
		}
	}
	r.t.Fatalf("no open question on %s in %+v", draftsPR, gs)
}

func (r *draftsRig) book() dturn.Book { return r.d.Book(r.t, draftsPR) }

// lastOutcomeURL is the url of the last record of the pull request's newest turn.
func (r *draftsRig) lastOutcomeURL() string {
	b := r.book()
	if len(b.Turns) == 0 {
		return ""
	}
	rec, ok := b.LastRecord(b.Turns[len(b.Turns)-1].Seq)
	if !ok || rec.Record.Outcome == nil {
		return ""
	}
	return rec.Record.Outcome.URL
}

func (r *draftsRig) wantPosted(want ...string) {
	r.t.Helper()
	if got := r.pub.posted(); !slices.Equal(got, want) {
		r.t.Fatalf("posted %q, want %q", got, want)
	}
}

func (r *draftsRig) wantState(id int64, state string) api.Draft {
	r.t.Helper()
	d := r.draft(id)
	if d.State != state {
		r.t.Fatalf("draft %d is %s (%s %s), want %s", id, d.State, d.Error, d.Reason, state)
	}
	return d
}

func choice(text string, opts ...string) string {
	return fmt.Sprintf(`{"kind":"choice","text":%q,"options":[%s]}`, text, strings.Join(opts, ","))
}

func draftOption(id, label string, d api.AgentDraft) string {
	return fmt.Sprintf(`{"id":%q,"label":%q,"consequence":"posts draft %d","draft":{"id":%d,"rev":%d}}`, id, label, d.ID, d.ID, d.Rev)
}

func doneOn(verdict string, d api.AgentDraft) string {
	return fmt.Sprintf(`{"state":"done","summary":"recorded %s","outcome":{"verdict":%q,"draft":{"id":%d,"rev":%d}}}`, verdict, verdict, d.ID, d.Rev)
}
