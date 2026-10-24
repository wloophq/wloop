package claude

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Shell runs a claude call with env applied over this process's environment,
// and returns its standard output and standard error together.
func Shell(ctx context.Context, dir string, env []string, stdin, name string, args ...string) (out []byte, err error) {
	defer meterSpawn(ctx, "shell", time.Now(), &err)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "CLAUDECODE=") })
	for _, kv := range env {
		key, _, set := strings.Cut(kv, "=")
		cmd.Env = slices.DeleteFunc(cmd.Env, func(o string) bool { return strings.HasPrefix(o, key+"=") })
		if set {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Stdin = strings.NewReader(stdin)
	return cmd.CombinedOutput()
}
