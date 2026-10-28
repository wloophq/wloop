package auth

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeSecret = `{"Access":"xoxe.xoxp-1-fakeaccess","Refresh":"xoxe-1-fakerefresh"}`

func TestSecurityKeychainKeepsTheSecretOffArgv(t *testing.T) {
	bin, dir := fakeSecurity(t)
	k := securityKeychain(t, bin)

	if err := k.Set("loop-test-slack", "current", []byte(fakeSecret)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	argv := readFile(t, filepath.Join(dir, "argv"))
	lines := strings.Split(strings.TrimSuffix(argv, "\n"), "\n")
	if lines[0] != "-i" {
		t.Fatalf("write argv = %q, want exactly -i", lines[0])
	}
	for _, l := range lines[1:] {
		if l != "find-generic-password -s loop-test-slack -a current -w" {
			t.Fatalf("unexpected call after the write: %q", l)
		}
	}
	stdin := readFile(t, filepath.Join(dir, "stdin"))
	want := "add-generic-password -U -s loop-test-slack -a current -X " + hex.EncodeToString([]byte(fakeSecret)) + "\n"
	if stdin != want {
		t.Fatalf("stdin = %q, want %q", stdin, want)
	}

	got, err := k.Get("loop-test-slack", "current")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != fakeSecret {
		t.Fatalf("Get = %q, want the secret back", got)
	}
	argv = readFile(t, filepath.Join(dir, "argv"))
	for _, secret := range []string{"fakeaccess", "fakerefresh", hex.EncodeToString([]byte(fakeSecret))} {
		if strings.Contains(argv, secret) {
			t.Fatalf("argv log holds the secret: %q", argv)
		}
	}

	if err := k.Delete("loop-test-slack", "current"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Get("loop-test-slack", "current"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := k.Delete("loop-test-slack", "current"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete of an absent item = %v, want ErrNotFound", err)
	}
}

func TestSecurityKeychainErrorsCarryNoSecret(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "security")
	script := "#!/bin/sh\ncat >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	k := securityKeychain(t, bin)
	err := k.Set("loop-test-slack", "current", []byte(fakeSecret))
	if err == nil {
		t.Fatal("Set succeeded against a failing binary")
	}
	assertNoToken(t, err.Error())
	if strings.Contains(err.Error(), hex.EncodeToString([]byte("xoxe"))) {
		t.Fatalf("error holds the hex secret: %v", err)
	}
}

func TestSecurityKeychainRefusesUnsafeNames(t *testing.T) {
	bin, _ := fakeSecurity(t)
	k := securityKeychain(t, bin)
	for _, name := range []string{"", "a b", "svc\n-X", `"q"`} {
		if err := k.Set(name, "current", []byte("x")); err == nil {
			t.Errorf("Set accepted service %q", name)
		}
	}
}

type recordingT struct {
	failed bool
	msg    string
}

func (r *recordingT) Helper() {}
func (r *recordingT) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
}

func TestKeychainHelperRefusesTheRealBinary(t *testing.T) {
	cases := map[string]string{"default": defaultSecurityBin, "empty": ""}
	if _, err := os.Stat(defaultSecurityBin); err == nil {
		link := filepath.Join(t.TempDir(), "sec")
		if err := os.Symlink(defaultSecurityBin, link); err != nil {
			t.Fatal(err)
		}
		cases["symlink"] = link
	}
	for name, bin := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &recordingT{}
			securityKeychain(rec, bin)
			if !rec.failed {
				t.Fatalf("helper accepted Bin %q", bin)
			}
		})
	}

	fake, _ := fakeSecurity(t)
	rec := &recordingT{}
	securityKeychain(rec, fake)
	if rec.failed {
		t.Fatalf("helper refused a fake: %s", rec.msg)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSecurityKeychainTimesOutAHungCall(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "security")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { securityTimeout = d }(securityTimeout)
	securityTimeout = 100 * time.Millisecond
	k := securityKeychain(t, bin)

	start := time.Now()
	_, err := k.Get("loop-test-slack", "current")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a hung security = %v, want a failure", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("Get took %v, want the timeout to end it", took)
	}
	if err := k.Set("loop-test-slack", "current", []byte(fakeSecret)); err == nil {
		t.Fatal("Set of a hung security succeeded")
	}
}
