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
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/domain/draft"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ghPublisher is the GitHub publisher of the drafts scenarios: it posts into a list, refuses a draft whose basis
// is not the live head as the real one re-reads it, and fails the next post with fail.
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
	return draft.CommentDelivery
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

// session is an agent session that took a turn: Handle is its process handle, which the agent route reads,
// Round the turn it runs.
type session struct{ Handle, Round string }

// servedDaemon is what the scenarios need of cli.ServeForTest (T13): a daemon built by serve's composition
// root, over the echo agent and a fake workspace manager, with the GitHub module's publisher and the plugins'
// live bases replaced.
type servedDaemon interface {
	Client() *client.Client
	// Review starts a review turn on url and the session that takes it.
	Review(t testing.TB, url string) session
	// Latest is the session that took the newest turn typed, once its session start ran.
	Latest(t testing.TB) session
	// EndTurn records raw as s's turn on url, as `turn end --file` does.
	EndTurn(t testing.TB, s session, url, raw string)
	// Send runs the outbox's sender once; Sweep runs the workflows once.
	Send(ctx context.Context) error
	Sweep(ctx context.Context) error
	Book(t testing.TB, u unit.Unit) dturn.Book
	// Instructions are the stored instructions of turn round.
	Instructions(t testing.TB, round string) string
}

// draftsRig is the scripted agent and the person of the scenarios, over a served daemon.
type draftsRig struct {
	t     *testing.T
	ctx   context.Context
	d     servedDaemon
	c     *client.Client
	pub   *ghPublisher
	bases *outboxtest.Bases
	s     session
}

// waits skips a scenario until the tasks it names have merged.
func waits(t *testing.T, on ...string) {
	t.Helper()
	t.Skip("T11 waits on " + strings.Join(on, "; "))
}

func newDraftsRig(t *testing.T) *draftsRig {
	t.Helper()
	r := &draftsRig{t: t, ctx: t.Context(), pub: &ghPublisher{head: "c2"}, bases: &outboxtest.Bases{}}
	r.bases.Set(draftsPR, "c2")
	r.d = serveForTest(t, r.pub, r.bases)
	r.c = r.d.Client()
	return r
}

// serveForTest becomes cli.ServeForTest(t, cli.TestOptions{Publishers: {github: pub}, Bases: bases,
// PRs: {draftsURL: "c2"}}) once T13 merges.
func serveForTest(t *testing.T, _ *ghPublisher, _ *outboxtest.Bases) servedDaemon {
	t.Skip("T11 waits on T13: cli.ServeForTest")
	return nil
}

func (r *draftsRig) review() { r.s = r.d.Review(r.t, draftsURL) }

func (r *draftsRig) resumed() { r.s = r.d.Latest(r.t) }

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

func (r *draftsRig) end(raw string) { r.d.EndTurn(r.t, r.s, draftsURL, raw) }

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

// answer is the person answering the open question on the pull request: an option's id picks it.
func (r *draftsRig) answer(text string) {
	r.t.Helper()
	gs, err := r.c.Gates(r.ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, g := range gs {
		if g.Kind != "question" || g.Unit != draftsPR.String() {
			continue
		}
		for _, in := range g.Intents {
			if in.Answer {
				if _, err := r.c.ResolveGate(r.ctx, g.ID, in.ID, api.TaskRequest{Args: map[string]string{"answer": text}, Source: "cli"}); err != nil {
					r.t.Fatalf("answering %q: %v", text, err)
				}
				return
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
