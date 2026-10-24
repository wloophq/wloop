// Package ghcli shells out to gh, which owns authentication.
package ghcli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/meter"
)

type Runner interface {
	// Run returns stdout even when gh exits non-zero, so a GraphQL response
	// with partial errors still yields its data.
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type Exec struct{ Bin string }

func New() *Exec { return &Exec{Bin: "gh"} }

func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin := e.Bin
	if bin == "" {
		bin = "gh"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("gh %s: %s", summarize(args), msg)
	}
	return stdout.Bytes(), nil
}

func summarize(args []string) string {
	if len(args) > 3 {
		args = args[:3]
	}
	return strings.Join(args, " ")
}

func Authenticated(ctx context.Context, r Runner) error {
	_, err := r.Run(ctx, "auth", "status")
	return err
}

type metered struct{ r Runner }

// Metered counts every call of r as one spawned GitHub sample.
func Metered(r Runner) Runner { return metered{r} }

func (m metered) Run(ctx context.Context, args ...string) ([]byte, error) {
	start := time.Now()
	out, err := m.r.Run(ctx, args...)
	op, points := meterOp(args)
	s := meter.Sample{Res: meter.GitHub, Op: op, Spawn: true, Dur: time.Since(start), Points: points, Bytes: int64(len(out)), Err: err != nil && ctx.Err() == nil}
	if meter.Tracing(ctx) {
		s.Detail = strings.Join(args, " ")
	}
	meter.Add(ctx, s)
	return out, err
}

func meterOp(args []string) (string, int64) {
	if len(args) >= 2 && args[0] == "api" {
		if args[1] == "graphql" {
			return "graphql", 0
		}
		return "rest", 1
	}
	var words []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(words) == 2 {
			break
		}
		words = append(words, a)
	}
	return "gh " + strings.Join(words, " "), 0
}
