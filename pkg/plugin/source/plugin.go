package source

import (
	"context"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/source/pb"
)

// New adapts s to the plugin protocol. The plugin's main serves it:
//
//	plugin.Serve(map[plugin.Kind]plugin.Impl{source.Kind: source.New(s)})
//
// Every capability interface s does not implement answers [ErrUnsupported].
func New(s Source) plugin.Impl { return &impl{src: s} }

type impl struct {
	src Source

	mu     sync.Mutex
	broker *goplugin.GRPCBroker
	conn   *grpc.ClientConn
}

func (i *impl) Health(ctx context.Context) (plugin.Health, error) { return i.src.Health(ctx) }

func (i *impl) Start(ctx context.Context, req plugin.StartRequest) error {
	h := &hostClient{}
	if req.HostBroker != 0 {
		i.mu.Lock()
		broker := i.broker
		i.mu.Unlock()
		conn, err := broker.Dial(req.HostBroker)
		if err != nil {
			return err
		}
		i.mu.Lock()
		i.conn = conn
		i.mu.Unlock()
		h.c = pb.NewHostServiceClient(conn)
	}
	return i.src.Start(ctx, StartRequest{StateDir: req.StateDir, Host: h.host()})
}

func (i *impl) Stop(ctx context.Context) error {
	err := i.src.Stop(ctx)
	i.mu.Lock()
	conn := i.conn
	i.conn = nil
	i.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	return err
}

func (i *impl) Notify(ctx context.Context, units []plugin.Unit, topics []plugin.Topic) error {
	return i.src.Notify(ctx, units, topics)
}

func (i *impl) Register(s *grpc.Server, broker *goplugin.GRPCBroker) error {
	i.mu.Lock()
	i.broker = broker
	i.mu.Unlock()
	pb.RegisterSourceServiceServer(s, &server{src: i.src})
	return nil
}

// server answers SourceService by the capabilities its source implements.
type server struct {
	pb.UnimplementedSourceServiceServer
	src Source
}

func serve[C, Resp any](ctx context.Context, src Source, what string, call *pb.Call, f func(context.Context, C) (Resp, error)) (Resp, error) {
	var zero Resp
	c, ok := src.(C)
	if !ok {
		return zero, unsupported(what)
	}
	resp, err := f(WithCall(ctx, callGo(call)), c)
	if err != nil {
		return zero, toStatus(err)
	}
	return resp, nil
}

func (s *server) Capabilities(context.Context, *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	return &pb.CapabilitiesResponse{Capabilities: mapSlice(Capabilities(s.src), func(c Capability) string { return string(c) })}, nil
}

func (s *server) Ingest(ctx context.Context, r *pb.IngestRequest) (*pb.IngestResponse, error) {
	return serve[Ingester](ctx, s.src, "ingester", r.GetCall(), func(ctx context.Context, c Ingester) (*pb.IngestResponse, error) {
		res, err := c.Ingest(ctx, IngestRequest{Job: r.GetJob(), Now: tm(r.GetNow())})
		return &pb.IngestResponse{Summary: res.Summary, Skipped: res.Skipped}, err
	})
}

func (s *server) Track(ctx context.Context, r *pb.TrackRequest) (*pb.TrackResponse, error) {
	return serve[Ingester](ctx, s.src, "ingester", r.GetCall(), func(ctx context.Context, c Ingester) (*pb.TrackResponse, error) {
		t, err := c.Track(ctx, unitGo(r.GetUnit()))
		return &pb.TrackResponse{Unit: unitPB(t.Unit), State: t.State}, err
	})
}

func (s *server) Untrack(ctx context.Context, r *pb.UntrackRequest) (*pb.UntrackResponse, error) {
	return serve[Ingester](ctx, s.src, "ingester", r.GetCall(), func(ctx context.Context, c Ingester) (*pb.UntrackResponse, error) {
		return &pb.UntrackResponse{}, c.Untrack(ctx, unitGo(r.GetUnit()))
	})
}

func (s *server) Resolve(ctx context.Context, r *pb.ResolveRequest) (*pb.ResolveResponse, error) {
	return serve[Ingester](ctx, s.src, "ingester", r.GetCall(), func(ctx context.Context, c Ingester) (*pb.ResolveResponse, error) {
		u, ok, err := c.Resolve(ctx, r.GetAddress())
		return &pb.ResolveResponse{Unit: unitPB(u), Ok: ok}, err
	})
}

func (s *server) Locate(ctx context.Context, r *pb.LocateRequest) (*pb.LocateResponse, error) {
	return serve[Ingester](ctx, s.src, "ingester", r.GetCall(), func(ctx context.Context, c Ingester) (*pb.LocateResponse, error) {
		u, ok, err := c.Locate(ctx, r.GetUrl())
		return &pb.LocateResponse{Unit: unitPB(u), Ok: ok}, err
	})
}

func (s *server) Action(ctx context.Context, r *pb.ActionRequest) (*pb.ActionResponse, error) {
	return serve[Presenter](ctx, s.src, "presenter", r.GetCall(), func(ctx context.Context, c Presenter) (*pb.ActionResponse, error) {
		res, err := c.Action(ctx, ActionRequest{Unit: unitGo(r.GetUnit()), Action: r.GetAction(), Input: r.GetInput(), Args: raw(r.GetArgsJson())})
		return &pb.ActionResponse{ResultJson: res}, err
	})
}

func (s *server) View(ctx context.Context, r *pb.ViewRequest) (*pb.ViewResponse, error) {
	return serve[Presenter](ctx, s.src, "presenter", r.GetCall(), func(ctx context.Context, c Presenter) (*pb.ViewResponse, error) {
		res, err := c.View(ctx, ViewRequest{View: r.GetView(), Query: raw(r.GetQueryJson())})
		return &pb.ViewResponse{ContentJson: res}, err
	})
}

func (s *server) Identity(ctx context.Context, r *pb.IdentityRequest) (*pb.IdentityResponse, error) {
	return serve[Presenter](ctx, s.src, "presenter", r.GetCall(), func(ctx context.Context, c Presenter) (*pb.IdentityResponse, error) {
		id, err := c.Identity(ctx)
		return &pb.IdentityResponse{Viewer: personPB(id.Viewer), Teams: id.Teams}, err
	})
}

func (s *server) Validate(ctx context.Context, r *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	return serve[Publisher](ctx, s.src, "publisher", r.GetCall(), func(ctx context.Context, c Publisher) (*pb.ValidateResponse, error) {
		return &pb.ValidateResponse{}, c.Validate(ctx, draftGo(r.GetDraft()))
	})
}

func (s *server) Where(ctx context.Context, r *pb.WhereRequest) (*pb.WhereResponse, error) {
	return serve[Publisher](ctx, s.src, "publisher", r.GetCall(), func(ctx context.Context, c Publisher) (*pb.WhereResponse, error) {
		w, err := c.Where(ctx, draftGo(r.GetDraft()))
		return &pb.WhereResponse{Where: w}, err
	})
}

func (s *server) Place(ctx context.Context, r *pb.PlaceRequest) (*pb.PlaceResponse, error) {
	return serve[Publisher](ctx, s.src, "publisher", r.GetCall(), func(ctx context.Context, c Publisher) (*pb.PlaceResponse, error) {
		t, err := c.Place(ctx, draftGo(r.GetDraft()))
		return &pb.PlaceResponse{Target: t}, err
	})
}

func (s *server) Publish(ctx context.Context, r *pb.PublishRequest) (*pb.PublishResponse, error) {
	return serve[Publisher](ctx, s.src, "publisher", r.GetCall(), func(ctx context.Context, c Publisher) (*pb.PublishResponse, error) {
		e, err := c.Publish(ctx, draftGo(r.GetDraft()))
		return &pb.PublishResponse{Evidence: draftEvidencePB(e)}, err
	})
}

func (s *server) Verify(ctx context.Context, r *pb.VerifyRequest) (*pb.VerifyResponse, error) {
	return serve[Publisher](ctx, s.src, "publisher", r.GetCall(), func(ctx context.Context, c Publisher) (*pb.VerifyResponse, error) {
		e, ok, err := c.Verify(ctx, draftGo(r.GetDraft()), tm(r.GetStartedAt()))
		return &pb.VerifyResponse{Evidence: draftEvidencePB(e), Posted: ok}, err
	})
}

func (s *server) Landed(ctx context.Context, r *pb.LandedRequest) (*pb.LandedResponse, error) {
	return serve[Lander](ctx, s.src, "lander", r.GetCall(), func(ctx context.Context, c Lander) (*pb.LandedResponse, error) {
		u, ok, err := c.Landed(ctx, draftGo(r.GetDraft()))
		return &pb.LandedResponse{Unit: unitPB(u), Ok: ok}, err
	})
}

func (s *server) Checkout(ctx context.Context, r *pb.CheckoutRequest) (*pb.CheckoutResponse, error) {
	return serve[Lander](ctx, s.src, "lander", r.GetCall(), func(ctx context.Context, c Lander) (*pb.CheckoutResponse, error) {
		co, ok, err := c.Checkout(ctx, unitGo(r.GetUnit()))
		return &pb.CheckoutResponse{Repo: co.Repo, Branch: co.Branch, Head: co.Head, Ok: ok}, err
	})
}

func (s *server) WriteState(ctx context.Context, r *pb.WriteStateRequest) (*pb.WriteStateResponse, error) {
	return serve[Lander](ctx, s.src, "lander", r.GetCall(), func(ctx context.Context, c Lander) (*pb.WriteStateResponse, error) {
		e, err := c.WriteState(ctx, StateWrite{Unit: unitGo(r.GetUnit()), Task: r.GetTask(), State: r.GetState(), Category: r.GetCategory()})
		return &pb.WriteStateResponse{Evidence: draftEvidencePB(e)}, err
	})
}

func (s *server) BriefInput(ctx context.Context, r *pb.BriefInputRequest) (*pb.BriefInputResponse, error) {
	return serve[Briefer](ctx, s.src, "briefer", r.GetCall(), func(ctx context.Context, c Briefer) (*pb.BriefInputResponse, error) {
		in, err := c.BriefInput(ctx, unitGo(r.GetUnit()))
		return briefInputPB(in), err
	})
}

func (s *server) BriefRevision(ctx context.Context, r *pb.BriefRevisionRequest) (*pb.BriefRevisionResponse, error) {
	return serve[Briefer](ctx, s.src, "briefer", r.GetCall(), func(ctx context.Context, c Briefer) (*pb.BriefRevisionResponse, error) {
		rev, err := c.BriefRevision(ctx, unitGo(r.GetUnit()))
		return briefRevisionPB(rev), err
	})
}

func (s *server) BriefDescribe(ctx context.Context, r *pb.BriefDescribeRequest) (*pb.BriefDescribeResponse, error) {
	return serve[Briefer](ctx, s.src, "briefer", r.GetCall(), func(ctx context.Context, c Briefer) (*pb.BriefDescribeResponse, error) {
		line, err := c.BriefDescribe(ctx, briefInputGo(r.GetInput()))
		return &pb.BriefDescribeResponse{Line: line}, err
	})
}

func (s *server) BriefStale(ctx context.Context, r *pb.BriefStaleRequest) (*pb.BriefStaleResponse, error) {
	return serve[Briefer](ctx, s.src, "briefer", r.GetCall(), func(ctx context.Context, c Briefer) (*pb.BriefStaleResponse, error) {
		w := r.GetWritten()
		stale, err := c.BriefStale(ctx, BriefStaleRequest{
			Written: BriefWritten{At: briefRevisionGo(w.GetAt()), Version: w.GetVersion(), Error: w.GetError(), Failures: int(w.GetFailures()), AttemptedAt: tm(w.GetAttemptedAt())},
			Current: briefRevisionGo(r.GetCurrent()), Version: r.GetVersion(), Now: tm(r.GetNow()),
		})
		return &pb.BriefStaleResponse{Stale: stale}, err
	})
}

func (s *server) Plan(ctx context.Context, r *pb.PlanRequest) (*pb.PlanResponse, error) {
	return serve[Planner](ctx, s.src, "planner", r.GetCall(), func(ctx context.Context, c Planner) (*pb.PlanResponse, error) {
		p, err := c.Plan(ctx, unitGo(r.GetUnit()), roundStateGo(r.GetState()))
		return &pb.PlanResponse{Facts: factsPB(p.Facts), Due: ts(p.Due), Rank: p.Rank}, err
	})
}

func (s *server) Payload(ctx context.Context, r *pb.PayloadRequest) (*pb.PayloadResponse, error) {
	return serve[Planner](ctx, s.src, "planner", r.GetCall(), func(ctx context.Context, c Planner) (*pb.PayloadResponse, error) {
		p, err := c.Payload(ctx, PayloadRequest{
			Unit: unitGo(r.GetUnit()), Intent: r.GetIntent(), Role: r.GetRole(), Trigger: r.GetTrigger(), Inputs: raw(r.GetInputsJson()),
			Guidance: r.GetGuidance(), Local: r.GetLocal(), Owned: r.GetOwned(), Plain: r.GetPlain(),
		})
		return &pb.PayloadResponse{PayloadJson: p}, err
	})
}

func (s *server) ApplyEffect(ctx context.Context, r *pb.ApplyEffectRequest) (*pb.ApplyEffectResponse, error) {
	return serve[Planner](ctx, s.src, "planner", r.GetCall(), func(ctx context.Context, c Planner) (*pb.ApplyEffectResponse, error) {
		req := EffectRequest{Effect: r.GetEffect(), Unit: unitGo(r.GetUnit())}
		if t := r.GetTurn(); t != nil {
			req.Turn = &TurnRef{Seq: t.GetSeq(), ID: t.GetId(), Head: t.GetHead()}
		}
		if d := r.GetDraft(); d != nil {
			dr := draftGo(d)
			req.Draft = &dr
		}
		return &pb.ApplyEffectResponse{}, c.ApplyEffect(ctx, req)
	})
}

func (s *server) Proposals(ctx context.Context, r *pb.ProposalsRequest) (*pb.ProposalsResponse, error) {
	return serve[Planner](ctx, s.src, "planner", r.GetCall(), func(ctx context.Context, c Planner) (*pb.ProposalsResponse, error) {
		ds, err := c.Proposals(ctx, tm(r.GetNow()))
		return &pb.ProposalsResponse{Drafts: mapSlice(ds, draftPB)}, err
	})
}

func (s *server) PlacementDefault(ctx context.Context, r *pb.PlacementDefaultRequest) (*pb.PlacementDefaultResponse, error) {
	return serve[Planner](ctx, s.src, "planner", r.GetCall(), func(ctx context.Context, c Planner) (*pb.PlacementDefaultResponse, error) {
		p, ok, err := c.PlacementDefault(ctx, unitGo(r.GetUnit()), r.GetItem())
		return &pb.PlacementDefaultResponse{Placement: placementPB(p), Ok: ok}, err
	})
}

func (s *server) CallTool(ctx context.Context, r *pb.CallToolRequest) (*pb.CallToolResponse, error) {
	return serve[Tooler](ctx, s.src, "tooler", r.GetCall(), func(ctx context.Context, c Tooler) (*pb.CallToolResponse, error) {
		res, err := c.CallTool(ctx, ToolCall{Tool: r.GetTool(), Args: raw(r.GetArgsJson()), Session: r.GetSession(), Turn: r.GetTurn()})
		return &pb.CallToolResponse{ResultJson: res.Result, IsError: res.IsError}, err
	})
}

func (s *server) RunCLI(ctx context.Context, r *pb.RunCLIRequest) (*pb.RunCLIResponse, error) {
	return serve[CLIRunner](ctx, s.src, "CLI runner", r.GetCall(), func(ctx context.Context, c CLIRunner) (*pb.RunCLIResponse, error) {
		res, err := c.RunCLI(ctx, CLIRequest{Args: r.GetArgs(), Stdin: r.GetStdin()})
		return &pb.RunCLIResponse{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: int64(res.ExitCode)}, err
	})
}
