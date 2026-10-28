package plugin

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wloophq/wloop/pkg/plugin/pb"
)

const (
	// parentPoll is how often a plugin checks that its parent lives.
	parentPoll = time.Second
	// stopBudget bounds the kinds' Stop when the parent died.
	stopBudget = 5 * time.Second
)

// Serve serves impls, one per kind, and returns when the host ends the
// plugin. It exits the process when the parent that started it dies, after
// giving every kind's Stop up to five seconds: there is no reattach.
func Serve(impls map[Kind]Impl) {
	go watchParent(context.Background(), parentPoll, parentPID(os.Getenv(ParentEnv), os.Getppid), os.Getppid, func() {
		stopAll(impls, stopBudget)
		os.Exit(0)
	})
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig:  Handshake,
		VersionedPlugins: VersionedPlugins(impls),
		GRPCServer:       goplugin.DefaultGRPCServer,
		Logger:           Logger(),
	})
}

// VersionedPlugins is the go-plugin plugin set of each protocol version, one
// plugin per kind of impls. The plugin side passes its impls; the host passes
// the kinds it expects, each with a nil Impl.
func VersionedPlugins(impls map[Kind]Impl) map[int]goplugin.PluginSet {
	srv := &lifecycleServer{impls: impls}
	set := goplugin.PluginSet{}
	for k, impl := range impls {
		set[string(k)] = &kindPlugin{kind: k, impl: impl, srv: srv}
	}
	return map[int]goplugin.PluginSet{Protocol: set}
}

// parentPID is the pid the host passed in env, or ppid's answer when the
// plugin runs without one.
func parentPID(env string, ppid func() int) int {
	if pid, err := strconv.Atoi(env); err == nil && pid > 0 {
		return pid
	}
	return ppid()
}

// watchParent calls gone once ppid no longer returns parent, checking every
// interval until ctx ends.
func watchParent(ctx context.Context, every time.Duration, parent int, ppid func() int, gone func()) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if ppid() != parent {
				gone()
				return
			}
		}
	}
}

func stopAll(impls map[Kind]Impl, budget time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	for k, impl := range impls {
		if err := impl.Stop(ctx); err != nil {
			Logger().Error("stop after the parent died", "kind", string(k), "error", err)
		}
	}
}

type kindPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	kind Kind
	impl Impl
	srv  *lifecycleServer
}

func (p *kindPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	p.srv.once.Do(func() { pb.RegisterPluginServiceServer(s, p.srv) })
	if p.impl == nil {
		return nil
	}
	return p.impl.Register(s, broker)
}

func (p *kindPlugin) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	return &Client{kind: p.kind, lc: pb.NewPluginServiceClient(conn), conn: conn, broker: broker}, nil
}

// lifecycleServer is the one lifecycle service of a plugin's server; it
// routes each request to the kind it names.
type lifecycleServer struct {
	pb.UnimplementedPluginServiceServer
	once  sync.Once
	impls map[Kind]Impl
}

func (s *lifecycleServer) impl(kind string) (Impl, error) {
	if impl := s.impls[Kind(kind)]; impl != nil {
		return impl, nil
	}
	return nil, status.Errorf(codes.NotFound, "kind %q is not served", kind)
}

func (s *lifecycleServer) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	impl, err := s.impl(req.GetKind())
	if err != nil {
		return nil, err
	}
	h, err := impl.Health(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.HealthResponse{Status: pb.Status(h.Status), Reason: h.Reason}, nil
}

func (s *lifecycleServer) Start(ctx context.Context, req *pb.StartRequest) (*pb.StartResponse, error) {
	impl, err := s.impl(req.GetKind())
	if err != nil {
		return nil, err
	}
	if err := impl.Start(ctx, StartRequest{StateDir: req.GetStateDir(), HostBroker: req.GetHostBroker()}); err != nil {
		return nil, toStatus(err)
	}
	return &pb.StartResponse{}, nil
}

func (s *lifecycleServer) Stop(ctx context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	impl, err := s.impl(req.GetKind())
	if err != nil {
		return nil, err
	}
	if err := impl.Stop(ctx); err != nil {
		return nil, toStatus(err)
	}
	return &pb.StopResponse{}, nil
}

func (s *lifecycleServer) Notify(ctx context.Context, req *pb.NotifyRequest) (*pb.NotifyResponse, error) {
	impl, err := s.impl(req.GetKind())
	if err != nil {
		return nil, err
	}
	units := make([]Unit, len(req.GetUnits()))
	for i, u := range req.GetUnits() {
		units[i] = Unit{Provider: u.GetProvider(), Key: u.GetKey()}
	}
	topics := make([]Topic, len(req.GetTopics()))
	for i, t := range req.GetTopics() {
		topics[i] = Topic(t)
	}
	if err := impl.Notify(ctx, units, topics); err != nil {
		return nil, toStatus(err)
	}
	return &pb.NotifyResponse{}, nil
}

func toStatus(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	return status.Error(codes.Unknown, err.Error())
}
