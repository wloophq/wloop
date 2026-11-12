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
	ToolDraft   = "github_draft"
	ToolThreads = "github_threads"
)

// The kinds github_draft proposes, as plugin.toml declares them.
const (
	kindComment       = "comment"
	kindReview        = "review"
	kindThreadReply   = "thread_reply"
	kindResolve       = "resolve"
	kindRequestReview = "request_review"
)

var draftKinds = []string{kindComment, kindReview, kindThreadReply, kindResolve, kindRequestReview}

// The states a draft's gate leaves it in that github_draft answers apart.
const (
	stateQueued  = "queued"
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
	case ToolDraft:
		return r.draft(ctx, call), nil
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

type draftArgs struct {
	Kind    string          `json:"kind"`
	Target  string          `json:"target"`
	Text    string          `json:"text"`
	Payload json.RawMessage `json:"payload"`
	Basis   string          `json:"basis"`
	Hold    bool            `json:"hold"`
	Effects []string        `json:"effects"`
	Why     string          `json:"why"`
}

// problems are what keeps a's draft from fitting its kind.
func (a draftArgs) problems() []problem {
	if !slices.Contains(draftKinds, a.Kind) {
		return []problem{{Answer: "problem_kind", Fields: fields{"Kinds": strings.Join(draftKinds, ", ")}}}
	}
	var out []problem
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

// draft proposes a GitHub draft on the pull request of the session's turn,
// once a review's basis is its head now.
func (r *running) draft(ctx context.Context, call src.ToolCall) src.ToolResult {
	var a draftArgs
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
	got, err := r.host.Gates.OpenGate(ctx, src.GateRequest{Draft: d, Session: call.Session})
	if err != nil {
		return failed(err)
	}
	name := "proposed"
	switch got.State {
	case stateQueued:
		name = "approved"
	case stateDropped:
		name = "dry_run"
	}
	return answer(name, fields{"ID": got.Draft, "Rev": 1, "Unit": ref.String()}, false)
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
