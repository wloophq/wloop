package auth

import (
	"os"
	"testing"
	"time"
)

// Under a loaded machine the first exec of a freshly written fake security script can take
// seconds, so the production timeout would fail tests whose fake answers at once.
func TestMain(m *testing.M) {
	securityTimeout = 2 * time.Minute
	os.Exit(m.Run())
}
