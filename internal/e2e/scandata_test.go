package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/gitx"
)

func scanScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "scripts", "scan-real-data.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitx.Binary(), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// scanRepo is a repository with a clean base commit; it returns the repo and the base.
func scanRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("plain text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")
	return dir, git(t, dir, "rev-parse", "HEAD")
}

func runScan(t *testing.T, dir, list, base string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", scanScript(t), base)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LOOP_DENY_LIST="+list)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func TestScanRealDataFlagsADeniedTerm(t *testing.T) {
	dir, base := scanRepo(t)
	list := filepath.Join(t.TempDir(), "deny.txt")
	if err := os.WriteFile(list, []byte("Zorblax Quux\nU0SYNTH42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runScan(t, dir, list, base); code != 0 {
		t.Fatalf("clean range: exit %d\n%s", code, out)
	}
	if err := os.MkdirAll(filepath.Join(dir, "fixtures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixtures", "t.json"), []byte(`{"user": "u0synth42", "name": "zorblax quux"}`+"\n"+`{"user": "U0SYNTH42"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "add a fixture")
	out, code := runScan(t, dir, list, base)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "fixtures/t.json: 3") {
		t.Errorf("the file and its count are not named:\n%s", out)
	}
	for _, term := range []string{"zorblax", "quux", "synth42"} {
		if strings.Contains(strings.ToLower(out), term) {
			t.Errorf("the output names a denied term:\n%s", out)
		}
	}
}

func TestScanRealDataFlagsACommitMessage(t *testing.T) {
	dir, base := scanRepo(t)
	list := filepath.Join(t.TempDir(), "deny.txt")
	if err := os.WriteFile(list, []byte("Zorblax\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "thanks Zorblax")
	out, code := runScan(t, dir, list, base)
	if code != 1 || !strings.Contains(out, "message: 1") || strings.Contains(strings.ToLower(out), "zorblax") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestScanRealDataNeedsADenyList(t *testing.T) {
	dir, base := scanRepo(t)
	for name, list := range map[string]string{"unset": "", "missing": filepath.Join(t.TempDir(), "absent.txt")} {
		if out, code := runScan(t, dir, list, base); code != 2 {
			t.Errorf("%s: exit %d, want 2\n%s", name, code, out)
		}
	}
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runScan(t, dir, empty, base); code != 2 {
		t.Errorf("empty list: exit %d, want 2\n%s", code, out)
	}
}

func commitScanFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "add "+filepath.Base(filepath.Dir(p)))
}

func denyList(t *testing.T, terms string) string {
	t.Helper()
	list := filepath.Join(t.TempDir(), "deny.txt")
	if err := os.WriteFile(list, []byte(terms), 0o600); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestScanRealDataFlagsAPathWithoutNamingIt(t *testing.T) {
	dir, base := scanRepo(t)
	git(t, dir, "config", "diff.noprefix", "true")
	commitScanFile(t, dir, "fx/zorblax-thread.json", []byte("{}\n"))
	out, code := runScan(t, dir, denyList(t, "Zorblax\n"), base)
	if code != 1 || !strings.Contains(out, " file 1: 1") || strings.Contains(strings.ToLower(out), "zorblax") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	commitScanFile(t, dir, "fx/zorblax-thread.json", []byte("{\"who\": \"zorblax\"}\n"))
	out, code = runScan(t, dir, denyList(t, "Zorblax\n"), base)
	if code != 1 || strings.Contains(strings.ToLower(out), "zorblax") {
		t.Fatalf("content in a denied path: exit %d\n%s", code, out)
	}
	git(t, dir, "mv", "fx/zorblax-thread.json", "fx/renamed.json")
	git(t, dir, "commit", "-qm", "rename")
	if out, code := runScan(t, dir, denyList(t, "Zorblax\n"), git(t, dir, "rev-parse", "HEAD~1")); code != 1 || !strings.Contains(out, "fx/renamed.json: 1") || strings.Contains(strings.ToLower(out), "zorblax") {
		t.Errorf("a rename carries the content to a clean path: exit %d\n%s", code, out)
	}
	if out, code := runScan(t, dir, denyList(t, "renamed\n"), git(t, dir, "rev-parse", "HEAD~1")); code != 1 || strings.Contains(out, "renamed") {
		t.Errorf("a rename to a denied path: exit %d\n%s", code, out)
	}
}

func TestScanRealDataReadsBinaryFiles(t *testing.T) {
	dir, base := scanRepo(t)
	commitScanFile(t, dir, "fx/blob.bin", append([]byte{0, 1, 2, 0, 'x'}, []byte("..Zorblax..\x00\xff\n")...))
	out, code := runScan(t, dir, denyList(t, "Zorblax\n"), base)
	if code != 1 || !strings.Contains(out, "fx/blob.bin: 1") || strings.Contains(strings.ToLower(out), "zorblax") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
