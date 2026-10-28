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

// DirRunner is a Runner that also runs gh in a directory, for the commands
// that read the checkout there.
type DirRunner interface {
	RunIn(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// RunIn runs gh in dir through r; "" is the process's directory.
func RunIn(ctx context.Context, r Runner, dir string, args ...string) ([]byte, error) {
	if dir == "" {
		return r.Run(ctx, args...)
	}
	d, ok := r.(DirRunner)
	if !ok {
		return nil, fmt.Errorf("gh %s: this runner cannot run in %s", summarize(args), dir)
	}
	return d.RunIn(ctx, dir, args...)
}

func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	return e.RunIn(ctx, "", args...)
}

func (e *Exec) RunIn(ctx context.Context, dir string, args ...string) ([]byte, error) {
	bin := e.Bin
	if bin == "" {
		bin = "gh"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.Bytes(), fmt.Errorf("gh %s: %w", summarize(args), err)
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
	return m.RunIn(ctx, "", args...)
}

func (m metered) RunIn(ctx context.Context, dir string, args ...string) ([]byte, error) {
	start := time.Now()
	out, err := RunIn(ctx, m.r, dir, args...)
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
