package claudedesktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/procgroup"
)

type Cmd struct {
	Name string
	Args []string
	Dir  string
	// Env is the whole environment; nil inherits the process's.
	Env []string
}

func (c Cmd) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

type Runner interface {
	Run(ctx context.Context, c Cmd) ([]byte, error)
}

type Exec struct{}

// errExecInTest keeps a test binary off the user's Claude Desktop.
var errExecInTest = errors.New("a test may not run claude or open: set Manager.Exec")

func (Exec) Run(ctx context.Context, c Cmd) ([]byte, error) {
	if testing.Testing() && (c.Name == "claude" || c.Name == "open") {
		return nil, fmt.Errorf("%s: %w", c, errExecInTest)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := procgroup.Command(ctx, 5*time.Second, c.Name, c.Args...)
	cmd.Dir, cmd.Env = c.Dir, c.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s: %s", c, msg)
	}
	return stdout.Bytes(), nil
}

// Metered records one meter.Workspace sample, a spawn, per command of Next.
type Metered struct{ Next Runner }

func (m Metered) Run(ctx context.Context, c Cmd) ([]byte, error) {
	start := time.Now()
	out, err := m.Next.Run(ctx, c)
	op := c.Name
	if len(c.Args) > 0 && !strings.HasPrefix(c.Args[0], "-") {
		op += " " + c.Args[0]
	}
	x := meter.Sample{Res: meter.Workspace, Op: op, Spawn: true, Err: err != nil && ctx.Err() == nil, Dur: time.Since(start), Bytes: int64(len(out))}
	if meter.Tracing(ctx) {
		x.Detail = c.String()
	}
	meter.Add(ctx, x)
	return out, err
}

func (m *Manager) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return m.Exec.Run(ctx, Cmd{Name: "git", Args: append([]string{"-C", dir}, args...)})
}
