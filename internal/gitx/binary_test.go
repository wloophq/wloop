package gitx

import (
	"os"
	"os/exec"
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
	got := Binary()
	if got == "git" || got == xcrunShim {
		t.Fatalf("Binary() = %q; want the git xcrun resolves", got)
	}
	if out, err := exec.Command(got, "--version").Output(); err != nil || len(out) == 0 {
		t.Fatalf("%s --version: %v", got, err)
	}
}
