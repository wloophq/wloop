package source

import (
	"context"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/source/pb"
)

// HostServer serves a [Host] to one plugin on the plugin connection's
// broker.
type HostServer struct {
	id  uint32
	srv *grpc.Server
	wg  sync.WaitGroup
}

// ServeHost serves h on c's broker until Close. The host passes ID as the
// HostBroker of the kind's StartRequest.
func ServeHost(c *plugin.Client, h Host) (*HostServer, error) {
	id := c.Broker().NextId()
	l, err := c.Broker().Accept(id)
	if err != nil {
		return nil, err
	}
	hs := &HostServer{id: id, srv: grpc.NewServer(grpc.WaitForHandlers(true))}
	pb.RegisterHostServiceServer(hs.srv, &hostServer{h: h})
	hs.wg.Go(func() { _ = hs.srv.Serve(l) })
	return hs, nil
}

// ID is the broker id the plugin dials.
func (s *HostServer) ID() uint32 { return s.id }

// Close stops serving and waits for the server and the calls it serves to end.
func (s *HostServer) Close() {
	s.srv.Stop()
	s.wg.Wait()
}

type hostServer struct {
	pb.UnimplementedHostServiceServer
	h Host
}

func hostCall[T, Resp any](ctx context.Context, impl T, what string, f func(T) (Resp, error)) (Resp, error) {
	var zero Resp
	if any(impl) == nil {
		return zero, status.Errorf(codes.Unimplemented, "the host serves no %s", what)
	}
	resp, err := f(impl)
	if err != nil {
		return zero, toStatus(err)
	}
	return resp, nil
}

func (s *hostServer) Report(ctx context.Context, r *pb.ReportRequest) (*pb.ReportResponse, error) {
	return hostCall(ctx, s.h.Reporter, "reporter", func(h Reporter) (*pb.ReportResponse, error) {
		return &pb.ReportResponse{}, h.Report(ctx, mapSlice(r.GetSnapshots(), snapshotGo))
	})
}

func (s *hostServer) Changed(ctx context.Context, r *pb.ChangedRequest) (*pb.ChangedResponse, error) {
	return hostCall(ctx, s.h.Reporter, "reporter", func(h Reporter) (*pb.ChangedResponse, error) {
		return &pb.ChangedResponse{}, h.Changed(ctx, unitsGo(r.GetUnits()), mapSlice(r.GetTopics(), func(t string) plugin.Topic { return plugin.Topic(t) }))
	})
}

func (s *hostServer) ApplyItems(ctx context.Context, r *pb.ApplyItemsRequest) (*pb.ApplyItemsResponse, error) {
	return hostCall(ctx, s.h.Ledger, "ledger", func(h Ledger) (*pb.ApplyItemsResponse, error) {
		res, err := h.ApplyItems(ctx, mapSlice(r.GetOps(), itemOpGo))
		return &pb.ApplyItemsResponse{Results: mapSlice(res, func(x ItemResult) *pb.ItemResult { return &pb.ItemResult{Item: x.Item, Error: x.Error} })}, err
	})
}

func (s *hostServer) QueryItems(ctx context.Context, r *pb.QueryItemsRequest) (*pb.QueryItemsResponse, error) {
	return hostCall(ctx, s.h.Ledger, "ledger", func(h Ledger) (*pb.QueryItemsResponse, error) {
		its, err := h.QueryItems(ctx, ItemQuery{Units: unitsGo(r.GetUnits()), OpenOnly: r.GetOpenOnly()})
		return &pb.QueryItemsResponse{Items: itemsPB(its)}, err
	})
}

func (s *hostServer) Tasks(ctx context.Context, r *pb.TasksRequest) (*pb.TasksResponse, error) {
	return hostCall(ctx, s.h.Reader, "reader", func(h Reader) (*pb.TasksResponse, error) {
		ts, err := h.Tasks(ctx, unitsGo(r.GetUnits()))
		return &pb.TasksResponse{Tasks: mapSlice(ts, taskPB)}, err
	})
}

func (s *hostServer) Books(ctx context.Context, r *pb.BooksRequest) (*pb.BooksResponse, error) {
	return hostCall(ctx, s.h.Reader, "reader", func(h Reader) (*pb.BooksResponse, error) {
		bs, err := h.Books(ctx, unitsGo(r.GetUnits()))
		return &pb.BooksResponse{Books: mapSlice(bs, bookPB)}, err
	})
}

func (s *hostServer) RoundState(ctx context.Context, r *pb.RoundStateRequest) (*pb.RoundStateResponse, error) {
	return hostCall(ctx, s.h.Reader, "reader", func(h Reader) (*pb.RoundStateResponse, error) {
		st, err := h.RoundState(ctx, unitGo(r.GetUnit()))
		return &pb.RoundStateResponse{State: roundStatePB(st)}, err
	})
}

func (s *hostServer) Workspaces(ctx context.Context, r *pb.WorkspacesRequest) (*pb.WorkspacesResponse, error) {
	return hostCall(ctx, s.h.Reader, "reader", func(h Reader) (*pb.WorkspacesResponse, error) {
		l, err := h.Workspaces(ctx, r.GetRepo())
		return listingPB(l), err
	})
}

func (s *hostServer) Session(ctx context.Context, r *pb.SessionRequest) (*pb.SessionResponse, error) {
	return hostCall(ctx, s.h.Reader, "reader", func(h Reader) (*pb.SessionResponse, error) {
		se, err := h.Session(ctx, r.GetHandle())
		return &pb.SessionResponse{Unit: unitPB(se.Unit), Turn: se.Turn, TurnSeq: se.TurnSeq, Intent: se.Intent, Role: se.Role, AllowedEffects: se.AllowedEffects}, err
	})
}

func (s *hostServer) OpenGate(ctx context.Context, r *pb.OpenGateRequest) (*pb.OpenGateResponse, error) {
	return hostCall(ctx, s.h.Gates, "gates", func(h Gates) (*pb.OpenGateResponse, error) {
		g, err := h.OpenGate(ctx, GateRequest{Draft: draftGo(r.GetDraft()), Session: r.GetSession()})
		return &pb.OpenGateResponse{Gate: gateRefPB(g)}, err
	})
}

func (s *hostServer) RecordSent(ctx context.Context, r *pb.RecordSentRequest) (*pb.RecordSentResponse, error) {
	return hostCall(ctx, s.h.Gates, "gates", func(h Gates) (*pb.RecordSentResponse, error) {
		g, err := h.RecordSent(ctx, draftGo(r.GetDraft()), r.GetSession(), r.GetTool())
		return &pb.RecordSentResponse{Gate: gateRefPB(g)}, err
	})
}

func (s *hostServer) RehomeDrafts(ctx context.Context, r *pb.RehomeDraftsRequest) (*pb.RehomeDraftsResponse, error) {
	return hostCall(ctx, s.h.Gates, "gates", func(h Gates) (*pb.RehomeDraftsResponse, error) {
		n, err := h.RehomeDrafts(ctx, mapSlice(r.GetMoves(), func(m *pb.UnitMove) UnitMove {
			return UnitMove{From: unitGo(m.GetFrom()), To: unitGo(m.GetTo())}
		}))
		return &pb.RehomeDraftsResponse{Moved: int64(n)}, err
	})
}

func (s *hostServer) SetOverride(ctx context.Context, r *pb.SetOverrideRequest) (*pb.SetOverrideResponse, error) {
	return hostCall(ctx, s.h.Gates, "gates", func(h Gates) (*pb.SetOverrideResponse, error) {
		ok, err := h.SetOverride(ctx, unitGo(r.GetUnit()), r.GetSet(), r.GetIfMatch())
		return &pb.SetOverrideResponse{Written: ok}, err
	})
}

func (s *hostServer) RecordEvents(ctx context.Context, r *pb.RecordEventsRequest) (*pb.RecordEventsResponse, error) {
	return hostCall(ctx, s.h.Recorder, "recorder", func(h Recorder) (*pb.RecordEventsResponse, error) {
		return &pb.RecordEventsResponse{}, h.RecordEvents(ctx, mapSlice(r.GetEvents(), eventGo))
	})
}

func (s *hostServer) SetMarks(ctx context.Context, r *pb.SetMarksRequest) (*pb.SetMarksResponse, error) {
	return hostCall(ctx, s.h.Recorder, "recorder", func(h Recorder) (*pb.SetMarksResponse, error) {
		return &pb.SetMarksResponse{}, h.SetMarks(ctx, unitGo(r.GetUnit()), r.GetMarks())
	})
}

func (s *hostServer) PutBrief(ctx context.Context, r *pb.PutBriefRequest) (*pb.PutBriefResponse, error) {
	return hostCall(ctx, s.h.Recorder, "recorder", func(h Recorder) (*pb.PutBriefResponse, error) {
		return &pb.PutBriefResponse{}, h.PutBrief(ctx, Brief{Task: r.GetTask(), Unit: unitGo(r.GetUnit()), Version: r.GetVersion(), Body: raw(r.GetBriefJson())})
	})
}

func (s *hostServer) SetWake(ctx context.Context, r *pb.SetWakeRequest) (*pb.SetWakeResponse, error) {
	return hostCall(ctx, s.h.Wakes, "wakes", func(h Wakes) (*pb.SetWakeResponse, error) {
		return &pb.SetWakeResponse{}, h.SetWake(ctx, Wake{Unit: unitGo(r.GetUnit()), Task: r.GetTask(), Until: tm(r.GetUntil()), When: r.GetWhen()})
	})
}

func (s *hostServer) ClearWake(ctx context.Context, r *pb.ClearWakeRequest) (*pb.ClearWakeResponse, error) {
	return hostCall(ctx, s.h.Wakes, "wakes", func(h Wakes) (*pb.ClearWakeResponse, error) {
		return &pb.ClearWakeResponse{}, h.ClearWake(ctx, unitGo(r.GetUnit()), r.GetTask())
	})
}

func (s *hostServer) Ask(ctx context.Context, r *pb.AskRequest) (*pb.AskResponse, error) {
	return hostCall(ctx, s.h.Services, "services", func(h Services) (*pb.AskResponse, error) {
		res, err := h.Ask(ctx, AskRequest{Prompt: r.GetPrompt(), Schema: raw(r.GetSchemaJson()), Tier: r.GetTier(), MaxCostUSD: r.GetMaxCostUsd(), MeterScope: r.GetMeterScope()})
		return &pb.AskResponse{Text: res.Text, Model: res.Model, CostUsd: res.CostUSD, Duration: durationpb.New(res.Duration), Tokens: res.Tokens}, err
	})
}

func (s *hostServer) Meter(ctx context.Context, r *pb.MeterRequest) (*pb.MeterResponse, error) {
	return hostCall(ctx, s.h.Services, "services", func(h Services) (*pb.MeterResponse, error) {
		return &pb.MeterResponse{}, h.Meter(ctx, Sample{Scope: r.GetScope(), Resource: r.GetResource(), Op: r.GetOp(), Spawn: r.GetSpawn(),
			Duration: r.GetDuration().AsDuration(), Failed: r.GetFailed(), Points: r.GetPoints()})
	})
}

func (s *hostServer) Config(ctx context.Context, _ *pb.ConfigRequest) (*pb.ConfigResponse, error) {
	return hostCall(ctx, s.h.Services, "services", func(h Services) (*pb.ConfigResponse, error) {
		c, err := h.Config(ctx)
		return &pb.ConfigResponse{Config: configPB(c)}, err
	})
}

func (s *hostServer) Profile(ctx context.Context, r *pb.ProfileRequest) (*pb.ProfileResponse, error) {
	return hostCall(ctx, s.h.Services, "services", func(h Services) (*pb.ProfileResponse, error) {
		b, err := h.Profile(ctx, r.GetPath())
		return &pb.ProfileResponse{Content: b}, err
	})
}

func (s *hostServer) Events(ctx context.Context, r *pb.EventsRequest) (*pb.EventsResponse, error) {
	return hostCall(ctx, s.h.History, "history", func(h History) (*pb.EventsResponse, error) {
		evs, err := h.Events(ctx, EventQuery{Units: unitsGo(r.GetUnits()), Kinds: r.GetKinds(), Since: tm(r.GetSince())})
		return &pb.EventsResponse{Events: mapSlice(evs, eventPB)}, err
	})
}

// hostClient is the plugin's side of the broker; with no connection every
// call is ErrUnsupported.
type hostClient struct {
	c pb.HostServiceClient
}

func (h *hostClient) host() Host { return Host{h, h, h, h, h, h, h, h} }

func viaHost[Resp any](h *hostClient, f func(pb.HostServiceClient) (Resp, error)) (Resp, error) {
	var zero Resp
	if h.c == nil {
		return zero, ErrUnsupported
	}
	resp, err := f(h.c)
	if err != nil {
		return zero, fromStatus(err)
	}
	return resp, nil
}

func (h *hostClient) Report(ctx context.Context, snapshots []Snapshot) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.ReportResponse, error) {
		return c.Report(ctx, &pb.ReportRequest{Snapshots: mapSlice(snapshots, snapshotPB)})
	})
	return err
}

func (h *hostClient) Changed(ctx context.Context, units []plugin.Unit, topics []plugin.Topic) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.ChangedResponse, error) {
		return c.Changed(ctx, &pb.ChangedRequest{Units: unitsPB(units), Topics: mapSlice(topics, func(t plugin.Topic) string { return string(t) })})
	})
	return err
}

func (h *hostClient) ApplyItems(ctx context.Context, ops []ItemOp) ([]ItemResult, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.ApplyItemsResponse, error) {
		return c.ApplyItems(ctx, &pb.ApplyItemsRequest{Ops: mapSlice(ops, itemOpPB)})
	})
	return mapSlice(r.GetResults(), func(x *pb.ItemResult) ItemResult { return ItemResult{Item: x.GetItem(), Error: x.GetError()} }), err
}

func (h *hostClient) QueryItems(ctx context.Context, q ItemQuery) ([]Item, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.QueryItemsResponse, error) {
		return c.QueryItems(ctx, &pb.QueryItemsRequest{Units: unitsPB(q.Units), OpenOnly: q.OpenOnly})
	})
	return itemsGo(r.GetItems()), err
}

func (h *hostClient) Tasks(ctx context.Context, units []plugin.Unit) ([]Task, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.TasksResponse, error) {
		return c.Tasks(ctx, &pb.TasksRequest{Units: unitsPB(units)})
	})
	return mapSlice(r.GetTasks(), taskGo), err
}

func (h *hostClient) Books(ctx context.Context, units []plugin.Unit) ([]Book, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.BooksResponse, error) {
		return c.Books(ctx, &pb.BooksRequest{Units: unitsPB(units)})
	})
	return mapSlice(r.GetBooks(), bookGo), err
}

func (h *hostClient) RoundState(ctx context.Context, u plugin.Unit) (RoundState, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.RoundStateResponse, error) {
		return c.RoundState(ctx, &pb.RoundStateRequest{Unit: unitPB(u)})
	})
	return roundStateGo(r.GetState()), err
}

func (h *hostClient) Workspaces(ctx context.Context, repo string) (Listing, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.WorkspacesResponse, error) {
		return c.Workspaces(ctx, &pb.WorkspacesRequest{Repo: repo})
	})
	return listingGo(r), err
}

func (h *hostClient) Session(ctx context.Context, handle string) (Session, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.SessionResponse, error) {
		return c.Session(ctx, &pb.SessionRequest{Handle: handle})
	})
	return Session{Unit: unitGo(r.GetUnit()), Turn: r.GetTurn(), TurnSeq: r.GetTurnSeq(), Intent: r.GetIntent(), Role: r.GetRole(), AllowedEffects: r.GetAllowedEffects()}, err
}

func (h *hostClient) OpenGate(ctx context.Context, req GateRequest) (GateRef, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.OpenGateResponse, error) {
		return c.OpenGate(ctx, &pb.OpenGateRequest{Draft: draftPB(req.Draft), Session: req.Session})
	})
	return gateRefGo(r.GetGate()), err
}

func (h *hostClient) RecordSent(ctx context.Context, d Draft, session, tool string) (GateRef, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.RecordSentResponse, error) {
		return c.RecordSent(ctx, &pb.RecordSentRequest{Draft: draftPB(d), Session: session, Tool: tool})
	})
	return gateRefGo(r.GetGate()), err
}

func (h *hostClient) RehomeDrafts(ctx context.Context, moves []UnitMove) (int, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.RehomeDraftsResponse, error) {
		return c.RehomeDrafts(ctx, &pb.RehomeDraftsRequest{Moves: mapSlice(moves, func(m UnitMove) *pb.UnitMove {
			return &pb.UnitMove{From: unitPB(m.From), To: unitPB(m.To)}
		})})
	})
	return int(r.GetMoved()), err
}

func (h *hostClient) SetOverride(ctx context.Context, u plugin.Unit, set, ifMatch map[string]string) (bool, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.SetOverrideResponse, error) {
		return c.SetOverride(ctx, &pb.SetOverrideRequest{Unit: unitPB(u), Set: set, IfMatch: ifMatch})
	})
	return r.GetWritten(), err
}

func (h *hostClient) RecordEvents(ctx context.Context, events []Event) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.RecordEventsResponse, error) {
		return c.RecordEvents(ctx, &pb.RecordEventsRequest{Events: mapSlice(events, eventPB)})
	})
	return err
}

func (h *hostClient) SetMarks(ctx context.Context, u plugin.Unit, marks map[string]string) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.SetMarksResponse, error) {
		return c.SetMarks(ctx, &pb.SetMarksRequest{Unit: unitPB(u), Marks: marks})
	})
	return err
}

func (h *hostClient) PutBrief(ctx context.Context, b Brief) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.PutBriefResponse, error) {
		return c.PutBrief(ctx, &pb.PutBriefRequest{Task: b.Task, Unit: unitPB(b.Unit), Version: b.Version, BriefJson: b.Body})
	})
	return err
}

func (h *hostClient) SetWake(ctx context.Context, w Wake) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.SetWakeResponse, error) {
		return c.SetWake(ctx, &pb.SetWakeRequest{Unit: unitPB(w.Unit), Task: w.Task, Until: ts(w.Until), When: w.When})
	})
	return err
}

func (h *hostClient) ClearWake(ctx context.Context, u plugin.Unit, task string) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.ClearWakeResponse, error) {
		return c.ClearWake(ctx, &pb.ClearWakeRequest{Unit: unitPB(u), Task: task})
	})
	return err
}

func (h *hostClient) Ask(ctx context.Context, req AskRequest) (AskResult, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.AskResponse, error) {
		return c.Ask(ctx, &pb.AskRequest{Prompt: req.Prompt, SchemaJson: req.Schema, Tier: req.Tier, MaxCostUsd: req.MaxCostUSD, MeterScope: req.MeterScope})
	})
	if err != nil {
		return AskResult{}, err
	}
	return AskResult{Text: r.GetText(), Model: r.GetModel(), CostUSD: r.CostUsd, Duration: r.GetDuration().AsDuration(), Tokens: r.GetTokens()}, nil
}

func (h *hostClient) Meter(ctx context.Context, s Sample) error {
	_, err := viaHost(h, func(c pb.HostServiceClient) (*pb.MeterResponse, error) {
		return c.Meter(ctx, &pb.MeterRequest{Scope: s.Scope, Resource: s.Resource, Op: s.Op, Spawn: s.Spawn,
			Duration: durationpb.New(s.Duration), Failed: s.Failed, Points: s.Points})
	})
	return err
}

func (h *hostClient) Config(ctx context.Context) (Config, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.ConfigResponse, error) {
		return c.Config(ctx, &pb.ConfigRequest{})
	})
	return configGo(r.GetConfig()), err
}

func (h *hostClient) Profile(ctx context.Context, path string) ([]byte, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.ProfileResponse, error) {
		return c.Profile(ctx, &pb.ProfileRequest{Path: path})
	})
	return r.GetContent(), err
}

func (h *hostClient) Events(ctx context.Context, q EventQuery) ([]Event, error) {
	r, err := viaHost(h, func(c pb.HostServiceClient) (*pb.EventsResponse, error) {
		return c.Events(ctx, &pb.EventsRequest{Units: unitsPB(q.Units), Kinds: q.Kinds, Since: ts(q.Since)})
	})
	return mapSlice(r.GetEvents(), eventGo), err
}
