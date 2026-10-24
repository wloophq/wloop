package socket

import (
	"context"
	"time"

	"github.com/coder/websocket"
)

const (
	readLimit    = 16 << 20
	pingInterval = 30 * time.Second
	pingTimeout  = 10 * time.Second
)

// wsSocket pings the server so a silently dead link ends the pending Read.
type wsSocket struct {
	c    *websocket.Conn
	stop context.CancelFunc
}

func dialWebsocket(ctx context.Context, u string) (Socket, error) {
	c, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(readLimit)
	pctx, stop := context.WithCancel(context.Background())
	go keepAlive(pctx, c)
	return &wsSocket{c: c, stop: stop}, nil
}

func keepAlive(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, pingTimeout)
		err := c.Ping(pctx)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				_ = c.CloseNow()
			}
			return
		}
	}
}

func (s *wsSocket) Read(ctx context.Context) ([]byte, error) {
	_, data, err := s.c.Read(ctx)
	return data, err
}

func (s *wsSocket) Write(ctx context.Context, frame []byte) error {
	return s.c.Write(ctx, websocket.MessageText, frame)
}

func (s *wsSocket) Close(reason string) error {
	s.stop()
	return s.c.Close(websocket.StatusNormalClosure, reason)
}
