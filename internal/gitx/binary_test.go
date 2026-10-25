package gitx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBinaryKeepsAGitPATHNames(t *testing.T) {
	bin := t.TempDir()
	stub := filepath.Join(bin, "git")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho stub\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got := Binary(); got != "git" {
		t.Fatalf("Binary() = %q; want the git PATH names", got)
	}
}

func TestBinarySkipsTheXcrunShim(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the xcrun shim is macOS's")
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	for resolved, want := range map[string]string{"/opt/dev/usr/bin/git": "/opt/dev/usr/bin/git", "": "git"} {
		saved := developerGit
		developerGit = func() string { return resolved }
		got := Binary()
		developerGit = saved
		if got != want {
			t.Errorf("xcrun resolving %q: Binary() = %q, want %q", resolved, got, want)
		}
	}
}
