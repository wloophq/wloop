package inventory

import (
	"testing"

	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	"github.com/wloophq/wloop/internal/domain/message"
)

func TestMessageStoreContract(t *testing.T) {
	messagestest.Contract(t, func(t *testing.T) message.Store { return NewMessages(open(t).db) })
}
