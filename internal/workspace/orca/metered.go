package orca

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/meter"
)

const detailMax = 300

// MeteredRunner records one meter.Workspace sample, a spawn, per CLI command of Next.
type MeteredRunner struct{ Next Runner }

func (m MeteredRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	start := time.Now()
	out, err := m.Next.Run(ctx, args...)
	x := meter.Sample{Res: meter.Workspace, Op: argvHead(args), Spawn: true, Err: err != nil && ctx.Err() == nil,
		Dur: time.Since(start), Bytes: int64(len(out))}
	if meter.Tracing(ctx) {
		x.Detail = cut(strings.Join(args, " "))
	}
	meter.Add(ctx, x)
	return out, err
}

// MeteredRPC records one meter.Workspace sample per runtime call of Next, op "rpc <method>".
type MeteredRPC struct{ Next RPC }

func (m MeteredRPC) Call(ctx context.Context, method string, params any) ([]byte, error) {
	start := time.Now()
	out, err := m.Next.Call(ctx, method, params)
	x := meter.Sample{Res: meter.Workspace, Op: "rpc " + method, Err: err != nil && ctx.Err() == nil,
		Dur: time.Since(start), Bytes: int64(len(out))}
	if meter.Tracing(ctx) {
		raw, _ := json.Marshal(params)
		x.Detail = cut(method + " " + string(raw))
	}
	meter.Add(ctx, x)
	return out, err
}

// argvHead is the subcommand path of an orca argv, at most two words, such as "terminal send".
func argvHead(args []string) string {
	var head []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(head) == 2 {
			break
		}
		head = append(head, a)
	}
	if len(head) == 0 {
		return "orca"
	}
	return strings.Join(head, " ")
}

func cut(s string) string {
	if len(s) > detailMax {
		return s[:detailMax]
	}
	return s
}
