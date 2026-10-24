package config

import (
	"os"
	"testing"
)

func TestHomeHonorsLoopUserHome(t *testing.T) {
	real, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv(UserHomeVar, dir)
	if got := Home(); got != dir {
		t.Errorf("Home() = %q, want %q", got, dir)
	}
	t.Setenv(UserHomeVar, "relative/home")
	if got := Home(); got != real {
		t.Errorf("a relative %s: Home() = %q, want %q", UserHomeVar, got, real)
	}
}
