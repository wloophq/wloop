package automation_test

import (
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
)

func TestATransientWithoutAnErrorStillReads(t *testing.T) {
	if got := (automation.Transient{}).Error(); got != "transient failure" {
		t.Errorf("Error = %q", got)
	}
	cause := errors.New("manager unreachable")
	if err := error(automation.Transient{Err: cause}); err.Error() != cause.Error() || !errors.Is(err, cause) {
		t.Errorf("Transient{cause} = %v", err)
	}
}
