package ghcli_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/ghcli"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type fakeRunner struct{ err error }

func (f fakeRunner) Run(context.Context, ...string) ([]byte, error) { return []byte("out"), f.err }

func TestMeteredOps(t *testing.T) {
	cases := []struct {
		args   []string
		op     string
		points int64
	}{
		{[]string{"api", "graphql", "-f", "query=x"}, "graphql", 0},
		{[]string{"api", "users/x"}, "rest", 1},
		{[]string{"pr", "view", "https://github.com/acme/api/pull/1", "--json", "title"}, "gh pr view", 0},
		{[]string{"auth", "status"}, "gh auth status", 0},
		{[]string{"repo", "clone", "acme/api"}, "gh repo clone", 0},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			ctx, s := metertest.Start(t, context.Background())
			if _, err := ghcli.Metered(fakeRunner{}).Run(ctx, c.args...); err != nil {
				t.Fatal(err)
			}
			stat := s.Tally()[meter.Key{Res: meter.GitHub, Op: c.op}]
			if stat.Calls != 1 || stat.Spawns != 1 || stat.Points != c.points || stat.Errors != 0 || stat.Bytes != 3 {
				t.Fatalf("%s: %+v", c.op, stat)
			}
		})
	}
}

func TestMeteredCountsAFailure(t *testing.T) {
	ctx, s := metertest.Start(t, context.Background())
	_, err := ghcli.Metered(fakeRunner{err: errors.New("boom")}).Run(ctx, "auth", "status")
	if err == nil {
		t.Fatal("want the error back")
	}
	if got := metertest.Count(s, metertest.Limit{Res: meter.GitHub, Field: metertest.Errors}); got != 1 {
		t.Fatalf("errors = %d", got)
	}
}

func TestAMissingGhIsExecNotFound(t *testing.T) {
	_, err := (&ghcli.Exec{Bin: "test-no-such-gh"}).Run(context.Background(), "auth", "status")
	if !errors.Is(err, exec.ErrNotFound) || !strings.HasPrefix(err.Error(), "gh auth status: ") {
		t.Fatalf("err = %v", err)
	}
}
