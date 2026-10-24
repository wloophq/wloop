package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type memKeychain struct {
	mu      sync.Mutex
	items   map[string][]byte
	setErrs int
	sets    int
	gets    int
}

func newMemKeychain() *memKeychain { return &memKeychain{items: map[string][]byte{}} }

func (k *memKeychain) Get(service, account string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gets++
	v, ok := k.items[service+"/"+account]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (k *memKeychain) Set(service, account string, secret []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sets++
	if k.setErrs > 0 {
		k.setErrs--
		return errors.New("keychain is locked")
	}
	k.items[service+"/"+account] = append([]byte(nil), secret...)
	return nil
}

func (k *memKeychain) Delete(service, account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.items[service+"/"+account]; !ok {
		return ErrNotFound
	}
	delete(k.items, service+"/"+account)
	return nil
}

// fileKeychain shares items between processes through a directory.
type fileKeychain struct{ dir string }

func (k fileKeychain) path(service, account string) string {
	return filepath.Join(k.dir, service+"."+account)
}

func (k fileKeychain) Get(service, account string) ([]byte, error) {
	b, err := os.ReadFile(k.path(service, account))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

func (k fileKeychain) Set(service, account string, secret []byte) error {
	tmp := k.path(service, account) + ".tmp"
	if err := os.WriteFile(tmp, secret, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, k.path(service, account))
}

func (k fileKeychain) Delete(service, account string) error {
	err := os.Remove(k.path(service, account))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// securityKeychain builds a SecurityKeychain for a test, refusing the real binary.
func securityKeychain(t fataler, bin string) SecurityKeychain {
	t.Helper()
	if isRealSecurity(bin) {
		t.Fatalf("test SecurityKeychain.Bin %q resolves to the real security binary", bin)
	}
	return SecurityKeychain{Bin: bin}
}

func isRealSecurity(bin string) bool {
	if bin == "" {
		return true
	}
	for _, p := range []string{bin, resolve(bin)} {
		if filepath.Clean(p) == defaultSecurityBin || filepath.Clean(p) == resolve(defaultSecurityBin) {
			return true
		}
	}
	return false
}

func resolve(path string) string {
	if abs, err := filepath.EvalSymlinks(path); err == nil {
		return abs
	}
	return path
}

// fakeSecurity writes a script that mimics security over dir and logs each argv to dir/argv.
func fakeSecurity(t testing.TB) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "security")
	script := fmt.Sprintf(`#!/bin/sh
d=%q
echo "$@" >> "$d/argv"
item() { echo "$d/item.$1.$2"; }
case "$1" in
-i)
  read -r cmd rest
  echo "$cmd $rest" | sed 's/-X [0-9a-f]*/-X <hex>/' >> "$d/stdin-shape"
  printf '%%s\n' "$cmd $rest" > "$d/stdin"
  set -- $rest
  svc=; acct=; hex=
  while [ $# -gt 0 ]; do
    case "$1" in
    -s) svc=$2; shift 2;;
    -a) acct=$2; shift 2;;
    -X) hex=$2; shift 2;;
    *) shift;;
    esac
  done
  printf '%%s' "$hex" | xxd -r -p > "$(item "$svc" "$acct")"
  ;;
find-generic-password)
  f=$(item "$3" "$5")
  [ -f "$f" ] || { echo "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain." >&2; exit 44; }
  cat "$f"; echo
  ;;
delete-generic-password)
  f=$(item "$3" "$5")
  [ -f "$f" ] || exit 44
  rm "$f"
  ;;
*) exit 2;;
esac
`, dir)
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}
