package plugin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/wloophq/wloop/pkg/plugin/pb"
)

// Spec is what the host knows of a plugin before it starts it.
type Spec struct {
	// ID is the plugin's id, from its manifest.
	ID string
	// Path is the plugin's binary.
	Path string
	// Kinds are the kinds the manifest says the binary serves.
	Kinds []Kind
	// Env is added to the process's environment, [BrandEnv] among it.
	Env []string
	// Checksum is the SHA-256 the binary must have; nil when the binary is
	// trusted by its path.
	Checksum []byte
	// Logger receives the plugin's log, which it writes to stderr.
	Logger hclog.Logger
}

// Launcher starts plugin processes. The host's launcher starts a binary in
// the daemon's process group; plugintest's serves kinds in-process.
type Launcher interface {
	Launch(ctx context.Context, spec Spec) (Process, error)
}

// Process is a started plugin. Kind returns the host's handle on one of the
// kinds it serves, and errors for a kind its Spec does not name. Protocol is
// the negotiated protocol version. Exited tells whether the process ended,
// by Kill or by crashing. Kill stops it, and is safe to call more than once.
type Process interface {
	Kind(k Kind) (*Client, error)
	Protocol() int
	Exited() bool
	Kill()
}

// ClientConfig is the go-plugin client configuration that starts spec's
// binary through cmd: the handshake, the versioned plugin set of the kinds
// spec names, gRPC only, and the checksum when spec has one. The process's
// environment is cmd.Env (the host's when nil), then spec.Env, then
// [ParentEnv] set to the host's pid; a later key wins. The client is
// unmanaged: go-plugin would keep a managed one until CleanupClients, and the
// host kills each client itself.
func ClientConfig(spec Spec, cmd *exec.Cmd) *goplugin.ClientConfig {
	kinds := map[Kind]Impl{}
	for _, k := range spec.Kinds {
		kinds[k] = nil
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(append(slices.Clip(env), spec.Env...), ParentEnv+"="+strconv.Itoa(os.Getpid()))
	cfg := &goplugin.ClientConfig{
		HandshakeConfig:  Handshake,
		VersionedPlugins: VersionedPlugins(kinds),
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		SkipHostEnv:      true,
		Logger:           spec.Logger,
	}
	if spec.Checksum != nil {
		cfg.SecureConfig = &goplugin.SecureConfig{Checksum: spec.Checksum, Hash: sha256.New()}
	}
	return cfg
}

// Exec starts spec's binary through cmd and completes the handshake. The
// caller sets cmd's process attributes.
func Exec(spec Spec, cmd *exec.Cmd) (Process, error) {
	c := goplugin.NewClient(ClientConfig(spec, cmd))
	proto, err := c.Client()
	if err != nil {
		c.Kill()
		return nil, fmt.Errorf("start plugin %s: %w", spec.ID, err)
	}
	return &execProcess{client: c, proto: proto, kinds: spec.Kinds}, nil
}

type execProcess struct {
	client *goplugin.Client
	proto  goplugin.ClientProtocol
	kinds  []Kind
}

func (p *execProcess) Kind(k Kind) (*Client, error) { return Dispense(p.proto, p.kinds, k) }
func (p *execProcess) Protocol() int                { return p.client.NegotiatedVersion() }
func (p *execProcess) Exited() bool                 { return p.client.Exited() }
func (p *execProcess) Kill()                        { p.client.Kill() }

// Dispense returns the host's handle on kind k of a connected plugin whose
// spec names kinds.
func Dispense(proto goplugin.ClientProtocol, kinds []Kind, k Kind) (*Client, error) {
	if !slices.Contains(kinds, k) {
		return nil, fmt.Errorf("the plugin does not declare kind %q", k)
	}
	raw, err := proto.Dispense(string(k))
	if err != nil {
		return nil, err
	}
	c, ok := raw.(*Client)
	if !ok {
		return nil, errors.New("the plugin set is not this SDK's")
	}
	return c, nil
}

// Client is the host's handle on one kind a plugin serves. It answers the
// kind's [Lifecycle]; the kind's SDK package builds its own client on Conn
// and Broker.
type Client struct {
	kind   Kind
	lc     pb.PluginServiceClient
	conn   *grpc.ClientConn
	broker *goplugin.GRPCBroker
}

var _ Lifecycle = (*Client)(nil)

// Kind is the kind this client reaches.
func (c *Client) Kind() Kind { return c.kind }

// Conn is the connection to the plugin, shared by every kind it serves.
func (c *Client) Conn() *grpc.ClientConn { return c.conn }

// Broker is the connection's go-plugin broker, on which the host serves a
// kind's host services.
func (c *Client) Broker() *goplugin.GRPCBroker { return c.broker }

func (c *Client) Health(ctx context.Context) (Health, error) {
	r, err := c.lc.Health(ctx, &pb.HealthRequest{Kind: string(c.kind)})
	if err != nil {
		return Health{}, err
	}
	return Health{Status: Status(r.GetStatus()), Reason: r.GetReason()}, nil
}

func (c *Client) Start(ctx context.Context, req StartRequest) error {
	_, err := c.lc.Start(ctx, &pb.StartRequest{Kind: string(c.kind), StateDir: req.StateDir, HostBroker: req.HostBroker})
	return err
}

func (c *Client) Stop(ctx context.Context) error {
	_, err := c.lc.Stop(ctx, &pb.StopRequest{Kind: string(c.kind)})
	return err
}

func (c *Client) Notify(ctx context.Context, units []Unit, topics []Topic) error {
	us := make([]*pb.Unit, len(units))
	for i, u := range units {
		us[i] = &pb.Unit{Provider: u.Provider, Key: u.Key}
	}
	ts := make([]string, len(topics))
	for i, t := range topics {
		ts[i] = string(t)
	}
	_, err := c.lc.Notify(ctx, &pb.NotifyRequest{Kind: string(c.kind), Units: us, Topics: ts})
	return err
}
