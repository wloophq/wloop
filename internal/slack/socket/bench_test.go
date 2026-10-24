package socket

import (
	"context"
	"encoding/json"
	"testing"
)

type benchSocket struct {
	frame []byte
	n     int
}

func (s *benchSocket) Read(ctx context.Context) ([]byte, error) {
	if s.n == 0 {
		return nil, context.Canceled
	}
	s.n--
	return s.frame, nil
}

func (s *benchSocket) Write(context.Context, []byte) error { return nil }
func (s *benchSocket) Close(string) error                  { return nil }

func BenchmarkReadAckQueue(b *testing.B) {
	frame, _ := json.Marshal(event("00000000-0000-4000-8000-000000000001", "Ev1"))
	queue := make(chan Envelope, b.N+1)
	c := &Conn{Open: func(context.Context) (string, error) { return "wss://h.test/link", nil },
		Dial: func(context.Context, string) (Socket, error) { return &benchSocket{frame: frame, n: b.N}, nil }}
	b.ReportAllocs()
	b.ResetTimer()
	_, _ = c.connect(context.Background(), queue)
	if len(queue) != b.N {
		b.Fatalf("queued %d of %d", len(queue), b.N)
	}
}
