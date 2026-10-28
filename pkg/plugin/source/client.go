package source

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/source/pb"
)

// Client is the host's handle on a source plugin: every capability
// interface, each call answering [ErrUnsupported] when the plugin lacks the
// capability; Capabilities says which it has. Every request carries the
// [Call] of its context.
type Client struct {
	*plugin.Client
	c pb.SourceServiceClient
}

var (
	_ Ingester  = (*Client)(nil)
	_ Presenter = (*Client)(nil)
	_ Publisher = (*Client)(nil)
	_ Lander    = (*Client)(nil)
	_ Briefer   = (*Client)(nil)
	_ Planner   = (*Client)(nil)
	_ Tooler    = (*Client)(nil)
	_ CLIRunner = (*Client)(nil)
)

// NewClient is the source client of c, a dispensed source kind.
func NewClient(c *plugin.Client) *Client {
	return &Client{Client: c, c: pb.NewSourceServiceClient(c.Conn())}
}

func via[Resp any](ctx context.Context, f func(*pb.Call) (Resp, error)) (Resp, error) {
	resp, err := f(callPB(CallOf(ctx)))
	return resp, fromStatus(err)
}

// Capabilities are the capabilities the plugin implements.
func (c *Client) Capabilities(ctx context.Context) ([]Capability, error) {
	r, err := via(ctx, func(*pb.Call) (*pb.CapabilitiesResponse, error) {
		return c.c.Capabilities(ctx, &pb.CapabilitiesRequest{})
	})
	return mapSlice(r.GetCapabilities(), func(s string) Capability { return Capability(s) }), err
}

func (c *Client) Ingest(ctx context.Context, req IngestRequest) (IngestResult, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.IngestResponse, error) {
		return c.c.Ingest(ctx, &pb.IngestRequest{Call: call, Job: req.Job, Now: ts(req.Now)})
	})
	return IngestResult{Summary: r.GetSummary(), Skipped: r.GetSkipped()}, err
}

func (c *Client) Track(ctx context.Context, u plugin.Unit) (Tracked, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.TrackResponse, error) {
		return c.c.Track(ctx, &pb.TrackRequest{Call: call, Unit: unitPB(u)})
	})
	return Tracked{Unit: unitGo(r.GetUnit()), State: r.GetState()}, err
}

func (c *Client) Untrack(ctx context.Context, u plugin.Unit) error {
	_, err := via(ctx, func(call *pb.Call) (*pb.UntrackResponse, error) {
		return c.c.Untrack(ctx, &pb.UntrackRequest{Call: call, Unit: unitPB(u)})
	})
	return err
}

func (c *Client) Resolve(ctx context.Context, address string) (plugin.Unit, bool, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.ResolveResponse, error) {
		return c.c.Resolve(ctx, &pb.ResolveRequest{Call: call, Address: address})
	})
	return unitGo(r.GetUnit()), r.GetOk(), err
}

func (c *Client) Locate(ctx context.Context, url string) (plugin.Unit, bool, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.LocateResponse, error) {
		return c.c.Locate(ctx, &pb.LocateRequest{Call: call, Url: url})
	})
	return unitGo(r.GetUnit()), r.GetOk(), err
}

func (c *Client) Action(ctx context.Context, req ActionRequest) (json.RawMessage, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.ActionResponse, error) {
		return c.c.Action(ctx, &pb.ActionRequest{Call: call, Unit: unitPB(req.Unit), Action: req.Action, Input: req.Input, ArgsJson: req.Args})
	})
	return raw(r.GetResultJson()), err
}

func (c *Client) View(ctx context.Context, req ViewRequest) (json.RawMessage, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.ViewResponse, error) {
		return c.c.View(ctx, &pb.ViewRequest{Call: call, View: req.View, QueryJson: req.Query})
	})
	return raw(r.GetContentJson()), err
}

func (c *Client) Identity(ctx context.Context) (Identity, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.IdentityResponse, error) {
		return c.c.Identity(ctx, &pb.IdentityRequest{Call: call})
	})
	return Identity{Viewer: personGo(r.GetViewer()), Teams: r.GetTeams()}, err
}

func (c *Client) Validate(ctx context.Context, d Draft) error {
	_, err := via(ctx, func(call *pb.Call) (*pb.ValidateResponse, error) {
		return c.c.Validate(ctx, &pb.ValidateRequest{Call: call, Draft: draftPB(d)})
	})
	return err
}

func (c *Client) Where(ctx context.Context, d Draft) (string, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.WhereResponse, error) {
		return c.c.Where(ctx, &pb.WhereRequest{Call: call, Draft: draftPB(d)})
	})
	return r.GetWhere(), err
}

func (c *Client) Place(ctx context.Context, d Draft) (string, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.PlaceResponse, error) {
		return c.c.Place(ctx, &pb.PlaceRequest{Call: call, Draft: draftPB(d)})
	})
	return r.GetTarget(), err
}

func (c *Client) Publish(ctx context.Context, d Draft) (Evidence, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.PublishResponse, error) {
		return c.c.Publish(ctx, &pb.PublishRequest{Call: call, Draft: draftPB(d)})
	})
	return draftEvidenceGo(r.GetEvidence()), err
}

func (c *Client) Verify(ctx context.Context, d Draft, startedAt time.Time) (Evidence, bool, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.VerifyResponse, error) {
		return c.c.Verify(ctx, &pb.VerifyRequest{Call: call, Draft: draftPB(d), StartedAt: ts(startedAt)})
	})
	return draftEvidenceGo(r.GetEvidence()), r.GetPosted(), err
}

func (c *Client) Landed(ctx context.Context, d Draft) (plugin.Unit, bool, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.LandedResponse, error) {
		return c.c.Landed(ctx, &pb.LandedRequest{Call: call, Draft: draftPB(d)})
	})
	return unitGo(r.GetUnit()), r.GetOk(), err
}

func (c *Client) Checkout(ctx context.Context, u plugin.Unit) (Checkout, bool, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.CheckoutResponse, error) {
		return c.c.Checkout(ctx, &pb.CheckoutRequest{Call: call, Unit: unitPB(u)})
	})
	return Checkout{Repo: r.GetRepo(), Branch: r.GetBranch(), Head: r.GetHead()}, r.GetOk(), err
}

func (c *Client) WriteState(ctx context.Context, w StateWrite) (Evidence, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.WriteStateResponse, error) {
		return c.c.WriteState(ctx, &pb.WriteStateRequest{Call: call, Unit: unitPB(w.Unit), Task: w.Task, State: w.State, Category: w.Category})
	})
	return draftEvidenceGo(r.GetEvidence()), err
}

func (c *Client) BriefInput(ctx context.Context, u plugin.Unit) (BriefInput, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.BriefInputResponse, error) {
		return c.c.BriefInput(ctx, &pb.BriefInputRequest{Call: call, Unit: unitPB(u)})
	})
	return briefInputGo(r), err
}

func (c *Client) BriefRevision(ctx context.Context, u plugin.Unit) (BriefRevision, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.BriefRevisionResponse, error) {
		return c.c.BriefRevision(ctx, &pb.BriefRevisionRequest{Call: call, Unit: unitPB(u)})
	})
	return briefRevisionGo(r), err
}

func (c *Client) BriefDescribe(ctx context.Context, in BriefInput) (string, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.BriefDescribeResponse, error) {
		return c.c.BriefDescribe(ctx, &pb.BriefDescribeRequest{Call: call, Input: briefInputPB(in)})
	})
	return r.GetLine(), err
}

func (c *Client) BriefStale(ctx context.Context, req BriefStaleRequest) (bool, error) {
	w := req.Written
	r, err := via(ctx, func(call *pb.Call) (*pb.BriefStaleResponse, error) {
		return c.c.BriefStale(ctx, &pb.BriefStaleRequest{
			Call:    call,
			Written: &pb.BriefWritten{At: briefRevisionPB(w.At), Version: w.Version, Error: w.Error, Failures: int64(w.Failures), AttemptedAt: ts(w.AttemptedAt)},
			Current: briefRevisionPB(req.Current), Version: req.Version, Now: ts(req.Now),
		})
	})
	return r.GetStale(), err
}

func (c *Client) Plan(ctx context.Context, u plugin.Unit, st RoundState) (Plan, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.PlanResponse, error) {
		return c.c.Plan(ctx, &pb.PlanRequest{Call: call, Unit: unitPB(u), State: roundStatePB(st)})
	})
	return Plan{Facts: factsGo(r.GetFacts()), Due: tm(r.GetDue()), Rank: r.GetRank()}, err
}

func (c *Client) Payload(ctx context.Context, req PayloadRequest) (json.RawMessage, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.PayloadResponse, error) {
		return c.c.Payload(ctx, &pb.PayloadRequest{
			Call: call, Unit: unitPB(req.Unit), Intent: req.Intent, Role: req.Role, Trigger: req.Trigger, InputsJson: req.Inputs,
			Guidance: req.Guidance, Local: req.Local, Owned: req.Owned, Plain: req.Plain,
		})
	})
	return raw(r.GetPayloadJson()), err
}

func (c *Client) ApplyEffect(ctx context.Context, req EffectRequest) error {
	in := &pb.ApplyEffectRequest{Effect: req.Effect, Unit: unitPB(req.Unit)}
	if t := req.Turn; t != nil {
		in.Turn = &pb.TurnRef{Seq: t.Seq, Id: t.ID, Head: t.Head}
	}
	if d := req.Draft; d != nil {
		in.Draft = draftPB(*d)
	}
	_, err := via(ctx, func(call *pb.Call) (*pb.ApplyEffectResponse, error) {
		in.Call = call
		return c.c.ApplyEffect(ctx, in)
	})
	return err
}

func (c *Client) Proposals(ctx context.Context, now time.Time) ([]Draft, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.ProposalsResponse, error) {
		return c.c.Proposals(ctx, &pb.ProposalsRequest{Call: call, Now: ts(now)})
	})
	return mapSlice(r.GetDrafts(), draftGo), err
}

func (c *Client) PlacementDefault(ctx context.Context, u plugin.Unit, item string) (Placement, bool, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.PlacementDefaultResponse, error) {
		return c.c.PlacementDefault(ctx, &pb.PlacementDefaultRequest{Call: call, Unit: unitPB(u), Item: item})
	})
	return placementGo(r.GetPlacement()), r.GetOk(), err
}

func (c *Client) CallTool(ctx context.Context, tc ToolCall) (ToolResult, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.CallToolResponse, error) {
		return c.c.CallTool(ctx, &pb.CallToolRequest{Call: call, Tool: tc.Tool, ArgsJson: tc.Args, Session: tc.Session, Turn: tc.Turn})
	})
	return ToolResult{Result: raw(r.GetResultJson()), IsError: r.GetIsError()}, err
}

func (c *Client) RunCLI(ctx context.Context, req CLIRequest) (CLIResult, error) {
	r, err := via(ctx, func(call *pb.Call) (*pb.RunCLIResponse, error) {
		return c.c.RunCLI(ctx, &pb.RunCLIRequest{Call: call, Args: req.Args, Stdin: req.Stdin})
	})
	return CLIResult{Stdout: r.GetStdout(), Stderr: r.GetStderr(), ExitCode: int(r.GetExitCode())}, err
}
