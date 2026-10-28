package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const defaultSecurityBin = "/usr/bin/security"

// securityTimeout bounds one security call: a hung Keychain must not hold the refresh lock.
var securityTimeout = 10 * time.Second

// errSecItemNotFound is the exit status security uses for an absent item.
const errSecItemNotFound = 44

// safeName keeps service and account names inert on the `security -i` command line.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (k SecurityKeychain) bin() string {
	if k.Bin == "" {
		return defaultSecurityBin
	}
	return k.Bin
}

func (k SecurityKeychain) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, k.bin(), args...)
	cmd.WaitDelay = time.Second
	return cmd
}

func checkNames(service, account string) error {
	if !safeName.MatchString(service) || !safeName.MatchString(account) {
		return fmt.Errorf("keychain: service %q and account %q must match %s", service, account, safeName)
	}
	return nil
}

func (k SecurityKeychain) Get(service, account string) ([]byte, error) {
	if err := checkNames(service, account); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), securityTimeout)
	defer cancel()
	cmd := k.command(ctx, "find-generic-password", "-s", service, "-a", account, "-w")
	cmd.Stdout = &out
	if err := k.run(cmd, "read"); err != nil {
		return nil, k.failure(ctx, "read", service, account, err)
	}
	secret := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	if isHex(secret) {
		if decoded, err := hex.DecodeString(string(secret)); err == nil {
			return decoded, nil
		}
	}
	return secret, nil
}

// isHex reports whether security printed the secret hex-encoded, which it does
// for a secret it cannot print as text. Stored secrets are JSON or app-level tokens,
// never a plain hex string.
func isHex(b []byte) bool {
	if len(b) == 0 || len(b)%2 != 0 {
		return false
	}
	for _, c := range b {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(c)) {
			return false
		}
	}
	return true
}

func (k SecurityKeychain) Set(service, account string, secret []byte) error {
	if err := checkNames(service, account); err != nil {
		return err
	}
	line := "add-generic-password -U -s " + service + " -a " + account + " -X " + hex.EncodeToString(secret) + "\n"
	ctx, cancel := context.WithTimeout(context.Background(), securityTimeout)
	defer cancel()
	cmd := k.command(ctx, "-i")
	cmd.Stdin = strings.NewReader(line)
	if err := k.run(cmd, "write"); err != nil {
		return k.failure(ctx, "write", service, account, err)
	}
	// The exit status of security -i is not documented to reflect the command it read.
	got, err := k.Get(service, account)
	if err != nil || !bytes.Equal(got, secret) {
		return fmt.Errorf("keychain write %s/%s: the item did not read back", service, account)
	}
	return nil
}

func (k SecurityKeychain) Delete(service, account string) error {
	if err := checkNames(service, account); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), securityTimeout)
	defer cancel()
	cmd := k.command(ctx, "delete-generic-password", "-s", service, "-a", account)
	if err := k.run(cmd, "delete"); err != nil {
		return k.failure(ctx, "delete", service, account, err)
	}
	return nil
}

// run meters the spawn.
func (k SecurityKeychain) run(cmd *exec.Cmd, op string) error {
	start := time.Now()
	err := cmd.Run()
	var exit *exec.ExitError
	failed := err != nil && !(errors.As(err, &exit) && exit.ExitCode() == errSecItemNotFound)
	if k.Meter != nil {
		k.Meter("keychain "+op, failed, time.Since(start))
	}
	return err
}

// failure never carries the command's output, which may echo the secret.
func (k SecurityKeychain) failure(ctx context.Context, op, service, account string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("keychain %s %s/%s: security did not answer within %v", op, service, account, securityTimeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if exit.ExitCode() == errSecItemNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("keychain %s %s/%s: security exited with status %d", op, service, account, exit.ExitCode())
	}
	return fmt.Errorf("keychain %s %s/%s: %w", op, service, account, err)
}
