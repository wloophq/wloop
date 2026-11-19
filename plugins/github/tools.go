package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/publish"
)

// The tools of the plugin's manifest.
const (
	ToolPost    = "github_post"
	ToolThreads = "github_threads"
)

// The kinds github_post writes, as plugin.toml declares them.
const (
	kindComment       = "comment"
	kindReview        = "review"
	kindThreadReply   = "thread_reply"
	kindResolve       = "resolve"
	kindRequestReview = "request_review"
)

var draftKinds = []string{kindComment, kindReview, kindThreadReply, kindResolve, kindRequestReview}

// The states a draft's gate leaves it in that github_post answers apart.
const (
	stateHeld    = "held"
	stateQueued  = "queued"
	stateSending = "sending"
	stateSent    = "sent"
	stateDropped = "dropped"
)

var _ src.Tooler = (*Plugin)(nil)

type fields map[string]any

func answer(name string, f fields, isError bool) src.ToolResult {
	r := src.ToolResult{Answer: name, IsError: isError}
	if len(f) > 0 {
		r.Fields, _ = json.Marshal(f)
	}
	return r
}

type problem struct {
	Answer string `json:"answer"`
	Fields fields `json:"fields,omitempty"`
}

func failed(err error) src.ToolResult { return answer("failed", fields{"Error": err.Error()}, true) }

func decodeStrict(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return errors.New("text after the arguments")
	}
	return nil
}

// CallTool runs one of the manifest's tools for the session call.Session.
func (p *Plugin) CallTool(ctx context.Context, call src.ToolCall) (src.ToolResult, error) {
	r, err := p.running()
	if err != nil {
		return src.ToolResult{}, err
	}
	switch call.Tool {
	case ToolPost:
		return r.post(ctx, call), nil
	case ToolThreads:
		return r.threads(ctx, call), nil
	}
	return src.ToolResult{}, src.ErrNotFound
}

// sessionRef is the pull request of the session's turn.
func (r *running) sessionRef(ctx context.Context, session string) (pr.Ref, *src.ToolResult) {
	if session == "" {
		res := answer("refused", fields{"Error": "the call names no session"}, true)
		return pr.Ref{}, &res
	}
	if r.host.Reader == nil {
		res := failed(src.ErrUnsupported)
		return pr.Ref{}, &res
	}
	sess, err := r.host.Reader.Session(ctx, session)
	if err != nil {
		res := failed(err)
		return pr.Ref{}, &res
	}
	ref, ok := pr.RefOf(sess.Unit)
	if !ok {
		res := failed(errors.New("the session's turn is on no GitHub pull request"))
		return pr.Ref{}, &res
	}
	return ref, nil
}

type postArgs struct {
	Kind    string          `json:"kind"`
	Target  string          `json:"target"`
	Text    string          `json:"text"`
	Payload json.RawMessage `json:"payload"`
	Basis   string          `json:"basis"`
	Draft   *bool           `json:"draft"`
	Hold    bool            `json:"hold"`
	Effects []string        `json:"effects"`
	Why     string          `json:"why"`
}

// now says a asks to post at once rather than draft.
func (a postArgs) now() bool { return a.Draft != nil && !*a.Draft }

// problems are what keeps a's post from fitting its kind.
func (a postArgs) problems() []problem {
	if !slices.Contains(draftKinds, a.Kind) {
		return []problem{{Answer: "problem_kind", Fields: fields{"Kinds": strings.Join(draftKinds, ", ")}}}
	}
	var out []problem
	if a.Hold && a.now() {
		out = append(out, problem{Answer: "problem_hold"})
	}
	if (a.Kind == kindComment || a.Kind == kindThreadReply) && strings.TrimSpace(a.Text) == "" {
		out = append(out, problem{Answer: "problem_text_required"})
	}
	var err error
	switch a.Kind {
	case kindReview:
		_, err = publish.DecodeReview(a.Payload)
		if a.Basis == "" {
			out = append(out, problem{Answer: "problem_basis_required"})
		}
	case kindRequestReview:
		_, err = publish.DecodeReviewRequest(a.Payload)
	default:
		if p := strings.TrimSpace(string(a.Payload)); p != "" && p != "null" && p != "{}" {
			err = errors.New("this kind takes no payload")
		}
	}
	if err != nil {
		out = append(out, problem{Answer: "problem_payload", Fields: fields{"Error": err.Error()}})
	}
	return out
}

// post writes a GitHub post on the pull request of the session's turn, once a review's basis is its head now:
// a draft for the user, or, with draft false, posted at once when the user drives the session.
func (r *running) post(ctx context.Context, call src.ToolCall) src.ToolResult {
	var a postArgs
	if err := decodeStrict(call.Args, &a); err != nil {
		return answer("bad_arguments", fields{"Error": err.Error()}, true)
	}
	a.Kind = strings.TrimSpace(a.Kind)
	if ps := a.problems(); len(ps) > 0 {
		return answer("fix_these", fields{"Problems": ps}, true)
	}
	ref, refusal := r.sessionRef(ctx, call.Session)
	if refusal != nil {
		return *refusal
	}
	if r.host.Gates == nil {
		return failed(src.ErrUnsupported)
	}
	pub := r.publisher()
	if a.Kind == kindReview {
		head, err := pub.Head(ctx, ref)
		if err != nil {
			return failed(err)
		}
		if head != a.Basis {
			return answer("stale", fields{"Basis": a.Basis, "Head": head}, true)
		}
	}
	d := src.Draft{Unit: ref.Unit(), Kind: a.Kind, Target: strings.TrimSpace(a.Target), Text: a.Text, Payload: a.Payload, Basis: a.Basis,
		Hold: a.Hold, Effects: a.Effects, Why: a.Why}
	got, err := r.host.Gates.OpenGate(ctx, src.GateRequest{Draft: d, Session: call.Session, Post: a.now()})
	if err != nil {
		return failed(err)
	}
	return answer(postAnswer(a.now(), got.State), fields{"ID": got.Draft, "Rev": got.Rev, "Unit": ref.String(), "Link": got.Link}, false)
}

// postAnswer is github_post's answer for a post whose draft its gate left in state st, now when it asked to post
// at once: posting once it is on its way, waits when it waits for the user, drafted when it was asked to,
// rule_posts when a rule of the user's posts it, dry_run under a dry run.
func postAnswer(now bool, st string) string {
	switch {
	case st == stateDropped:
		return "dry_run"
	case now && st == stateHeld:
		return "waits"
	case now:
		return "posting"
	case st == stateQueued || st == stateSending || st == stateSent:
		return "rule_posts"
	}
	return "drafted"
}

// threads answers the unresolved review threads and the latest reviews of
// the pull request of the session's turn, as JSON.
func (r *running) threads(ctx context.Context, call src.ToolCall) src.ToolResult {
	var a struct{}
	if err := decodeStrict(call.Args, &a); err != nil {
		return answer("bad_arguments", fields{"Error": err.Error()}, true)
	}
	ref, refusal := r.sessionRef(ctx, call.Session)
	if refusal != nil {
		return *refusal
	}
	c, err := r.publisher().Conversation(ctx, ref)
	if err != nil {
		return failed(err)
	}
	raw, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return failed(err)
	}
	return answer("threads", fields{"Unit": ref.String(), "JSON": string(raw)}, false)
}
