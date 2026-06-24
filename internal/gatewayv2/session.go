package gatewayv2

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	ruleshiftv2 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv2"
)

var nextSessionID atomic.Uint64
var errSessionFull = errors.New("session send queue is full")

type session struct {
	id       uint64
	playerID string
	send     chan *ruleshiftv2.ServerEnvelope
	done     chan struct{}
	mu       sync.Mutex
	closed   bool
}

func newSession(playerID string, size int) *session {
	return &session{id: nextSessionID.Add(1), playerID: playerID, send: make(chan *ruleshiftv2.ServerEnvelope, size), done: make(chan struct{})}
}
func (s *session) Send(ctx context.Context, value *ruleshiftv2.ServerEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session closed")
	}
	if value.GetStateSnapshot() != nil {
		for {
			select {
			case <-s.send:
			default:
				goto compacted
			}
		}
	}
compacted:
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.send <- value:
		return nil
	default:
		return errSessionFull
	}
}
func (s *session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.send)
	close(s.done)
}
func (s *session) String() string { return fmt.Sprintf("session(%d,%s)", s.id, s.playerID) }
