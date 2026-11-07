package daemon

import "testing"

func TestQueueNarrowedByLens(t *testing.T) { t.Skip("B5 GET /api/queues/{kind}?q= narrows the queue") }

func TestQueueUnknownKind(t *testing.T) { t.Skip("B5 an unknown decision kind is 404") }
