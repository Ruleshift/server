package gateway

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"github.com/Ruleshift/server/internal/room"
)

var nextWebSocketSessionID atomic.Uint64

type websocketSession struct {
	sessionID   uint64
	playerID    string
	send        chan *ruleshiftv1.ServerEnvelope
	done        chan struct{}
	mu          sync.Mutex
	closed      bool
	closeReason string
}

func newWebSocketSession(sendQueueSize int) (*websocketSession, error) {
	if sendQueueSize <= 0 {
		return nil, fmt.Errorf("session send queue size must be positive")
	}
	return &websocketSession{
		sessionID: nextWebSocketSessionID.Add(1),
		send:      make(chan *ruleshiftv1.ServerEnvelope, sendQueueSize),
		done:      make(chan struct{}),
	}, nil
}

func (s *websocketSession) SessionID() uint64 {
	return s.sessionID
}

func (s *websocketSession) Bind(playerID string) error {
	if playerID == "" {
		return room.ErrEmptyPlayerID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.playerID = playerID
	return nil
}

func (s *websocketSession) PlayerID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.playerID
}

func (s *websocketSession) Send(ctx context.Context, msg *ruleshiftv1.ServerEnvelope) error {
	if msg == nil {
		return fmt.Errorf("server envelope must not be nil")
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("send server envelope: %w", ctx.Err())
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return room.ErrPlayerSinkClosed
	}
	if msg.GetStateSnapshot() != nil {
		s.compactLocked()
	}

	select {
	case s.send <- msg:
		return nil
	default:
		return room.ErrPlayerSinkFull
	}
}

func (s *websocketSession) Close(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true
	s.closeReason = reason
	close(s.send)
	close(s.done)
}

func (s *websocketSession) Outbound() <-chan *ruleshiftv1.ServerEnvelope {
	return s.send
}

func (s *websocketSession) Done() <-chan struct{} {
	return s.done
}

func (s *websocketSession) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *websocketSession) CloseReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeReason
}

func (s *websocketSession) QueueDepth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.send)
}

func (s *websocketSession) compactLocked() {
	for {
		select {
		case <-s.send:
		default:
			return
		}
	}
}
