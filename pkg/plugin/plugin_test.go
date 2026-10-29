package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

const (
	roleEnv    = "PLUGIN_TEST_ROLE"
	pidFileEnv = "PLUGIN_TEST_PID_FILE"
)

type fixedKind struct{ reason string }

func (k fixedKind) Health(context.Context) (Health, error) {
	return Health{Status: StatusServing, Reason: k.reason}, nil
}
func (fixedKind) Start(context.Context, StartRequest) error         { return nil }
func (fixedKind) Stop(context.Context) error                        { return nil }
func (fixedKind) Notify(context.Context, []Unit, []Topic) error     { return nil }
func (fixedKind) Register(*grpc.Server, *goplugin.GRPCBroker) error { return nil }

func TestMain(m *testing.M) {
	switch os.Getenv(roleEnv) {
	case "serve":
		Serve(map[Kind]Impl{"alpha": fixedKind{reason: "from the child"}})
		os.Exit(0)
	case "serve-env":
		reason := fmt.Sprintf("brand=%s path=%t home=%t", os.Getenv(BrandEnv), os.Getenv("PATH") != "", os.Getenv("HOME") != "")
		Serve(map[Kind]Impl{"alpha": fixedKind{reason: reason}})
		os.Exit(0)
	case "parent":
		os.Exit(runParent())
	}
	os.Exit(m.Run())
}

// runParent launches a plugin child, writes its pid, and waits to be killed.
func runParent() int {
	cmd := childCmd()
	if _, err := Exec(Spec{ID: "alpha-plugin", Kinds: []Kind{"alpha"}, Logger: hclog.NewNullLogger()}, cmd); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := os.WriteFile(os.Getenv(pidFileEnv), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		return 3
	}
	select {}
}

func childCmd() *exec.Cmd { return roleCmd("serve") }

func roleCmd(role string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), roleEnv+"="+role)
	return cmd
}

func TestSpecEnvReachesThePlugin(t *testing.T) {
	t.Setenv(BrandEnv, "fromhost")
	cmd := roleCmd("serve-env")
	cmd.Env = append(cmd.Env, BrandEnv+"=other")
	p, err := Exec(Spec{ID: "alpha-plugin", Kinds: []Kind{"alpha"}, Env: []string{BrandEnv + "=acme"}, Logger: hclog.NewNullLogger()}, cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Kill)
	c, err := p.Kind("alpha")
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Health(t.Context())
	if err != nil || h.Reason != "brand=acme path=true home=true" {
		t.Errorf("the plugin saw %q, %v", h.Reason, err)
	}
}

func TestACrashedPluginReportsExited(t *testing.T) {
	p, err := Exec(Spec{ID: "alpha-plugin", Kinds: []Kind{"alpha"}, Logger: hclog.NewNullLogger()}, childCmd())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Kill)
	c, err := p.Kind("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if p.Exited() {
		t.Fatal("a running plugin reports Exited")
	}
	pid := p.(*execProcess).client.ReattachConfig().Pid
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	poll(t, "Exited after the crash", p.Exited)
	if _, err := c.Health(t.Context()); err == nil {
		t.Error("a crashed plugin answered Health")
	}
}

func TestAKilledCrashedPluginLeavesNoClientBehind(t *testing.T) {
	if ClientConfig(Spec{}, childCmd()).Managed {
		t.Error("the client is managed: go-plugin keeps every managed client until CleanupClients, which the host never calls")
	}
	base := runtime.NumGoroutine()
	for range 3 {
		p, err := Exec(Spec{ID: "alpha-plugin", Kinds: []Kind{"alpha"}, Logger: hclog.NewNullLogger()}, childCmd())
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(p.(*execProcess).client.ReattachConfig().Pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		poll(t, "Exited after the crash", p.Exited)
		p.Kill()
	}
	poll(t, "the crashed plugins' goroutines to end", func() bool { return runtime.NumGoroutine() <= base })
}

func TestVersionedPluginsNegotiate(t *testing.T) {
	t.Run("the host and the plugin agree on protocol 1", func(t *testing.T) {
		p, err := Exec(Spec{ID: "alpha-plugin", Kinds: []Kind{"alpha"}, Logger: hclog.NewNullLogger()}, childCmd())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Kill)
		if p.Protocol() != Protocol {
			t.Errorf("negotiated %d, want %d", p.Protocol(), Protocol)
		}
		c, err := p.Kind("alpha")
		if err != nil {
			t.Fatal(err)
		}
		h, err := c.Health(t.Context())
		if err != nil || h != (Health{Status: StatusServing, Reason: "from the child"}) {
			t.Errorf("Health = %+v, %v", h, err)
		}
	})
	t.Run("a host that speaks only another version is refused", func(t *testing.T) {
		cfg := ClientConfig(Spec{ID: "alpha-plugin", Kinds: []Kind{"alpha"}, Logger: hclog.NewNullLogger()}, childCmd())
		cfg.VersionedPlugins = map[int]goplugin.PluginSet{Protocol + 1: VersionedPlugins(map[Kind]Impl{"alpha": nil})[Protocol]}
		c := goplugin.NewClient(cfg)
		t.Cleanup(c.Kill)
		if _, err := c.Client(); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("Client() = %v, want a version mismatch", err)
		}
	})
}

func TestAPluginExitsWhenItsParentDies(t *testing.T) {
	t.Run("the watch fires on the first poll after the parent changes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var ppid atomic.Int64
			ppid.Store(40)
			var gone time.Duration
			start := time.Now()
			done := make(chan struct{})
			go func() {
				defer close(done)
				watchParent(t.Context(), parentPoll, 40, func() int { return int(ppid.Load()) }, func() { gone = time.Since(start) })
			}()
			time.Sleep(3*parentPoll + parentPoll/2)
			synctest.Wait()
			if gone != 0 {
				t.Fatalf("fired at %v while the parent lived", gone)
			}
			ppid.Store(1)
			<-done
			if gone != 4*parentPoll {
				t.Errorf("fired at %v, want %v", gone, 4*parentPoll)
			}
		})
	})
	t.Run("a parent already gone when Serve starts", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			var gone time.Duration
			ctx, cancel := context.WithTimeout(t.Context(), 3*parentPoll)
			defer cancel()
			watchParent(ctx, parentPoll, 40, func() int { return 1 }, func() { gone = time.Since(start) })
			if gone != parentPoll {
				t.Errorf("fired at %v, want %v", gone, parentPoll)
			}
		})
	})
	t.Run("the parent is the pid the host passed, else the first ppid", func(t *testing.T) {
		for _, c := range []struct {
			env  string
			want int
		}{{"40", 40}, {"", 7}, {"x", 7}, {"0", 7}} {
			if got := parentPID(c.env, func() int { return 7 }); got != c.want {
				t.Errorf("parentPID(%q) = %d, want %d", c.env, got, c.want)
			}
		}
	})
	t.Run("the watch stops with its context", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				watchParent(ctx, parentPoll, 40, func() int { return 40 }, func() { t.Error("fired") })
			}()
			cancel()
			<-done
		})
	})
	t.Run("a real plugin process exits after its host is killed", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "child.pid")
		parent := exec.Command(os.Args[0], "-test.run=^$")
		parent.Env = append(os.Environ(), roleEnv+"=parent", pidFileEnv+"="+pidFile)
		if err := parent.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
		var child int
		poll(t, "the child's pid", func() bool {
			raw, err := os.ReadFile(pidFile)
			child, _ = strconv.Atoi(string(raw))
			return err == nil && child > 0
		})
		t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
		if !alive(child) {
			t.Fatal("the child is not running before its parent dies")
		}
		if err := parent.Process.Signal(syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		_ = parent.Wait()
		poll(t, "the child's exit", func() bool { return !alive(child) })
	})
}

// alive tells whether pid runs; a zombie waiting for its reaper has exited.
func alive(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false
	}
	stat := strings.TrimSpace(string(out))
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

// poll waits for cond outside any bubble; its deadline only guards a hang.
func poll(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
