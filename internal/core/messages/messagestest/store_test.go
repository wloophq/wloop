package messagestest

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/message"
)

func TestFakeStoreContract(t *testing.T) {
	Contract(t, func(*testing.T) message.Store { return New() })
}
