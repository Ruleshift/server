package room

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrSessionQueueFull = errors.New("session send queue is full")
	ErrSessionClosed    = errors.New("session is closed")
)

type CloseReason string

const (
	CloseReasonSlowConsumer CloseReason = "slow_consumer"
	CloseReasonShutdown     CloseReason = "shutdown"
	CloseReasonReplaced     CloseReason = "replaced"
)

type MessageKind uint8

const (
	MessageKindUnspecified MessageKind = iota
	MessageKindStateDelta
	MessageKindStateSnapshot
)

type RoomMessage struct {
	Kind     MessageKind
	Delta    StateDelta
	Snapshot StateSnapshot
}

type PlayerSession interface {
	PlayerID() string
	TrySend(message RoomMessage) error
	TrySendSnapshot(snapshot StateSnapshot) error
	IsClosed() bool
	Close(reason CloseReason)
}

type BoundedPlayerSession struct {
	playerID    string
	outbound    chan RoomMessage
	mu          sync.Mutex
	closed      bool
	closeReason CloseReason
}

func NewBoundedPlayerSession(playerID string, sendQueueSize int) (*BoundedPlayerSession, error) {
	if playerID == "" {
		return nil, ErrEmptyPlayerID
	}
	if sendQueueSize <= 0 {
		return nil, fmt.Errorf("session send queue size must be positive")
	}

	return &BoundedPlayerSession{
		playerID: playerID,
		outbound: make(chan RoomMessage, sendQueueSize),
	}, nil
}

func (s *BoundedPlayerSession) PlayerID() string {
	return s.playerID
}

func (s *BoundedPlayerSession) TrySend(message RoomMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSessionClosed
	}

	select {
	case s.outbound <- message:
		return nil
	default:
		return ErrSessionQueueFull
	}
}

func (s *BoundedPlayerSession) TrySendSnapshot(snapshot StateSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSessionClosed
	}

	for {
		select {
		case <-s.outbound:
			continue
		default:
		}
		break
	}

	select {
	case s.outbound <- RoomMessage{Kind: MessageKindStateSnapshot, Snapshot: snapshot}:
		return nil
	default:
		return ErrSessionQueueFull
	}
}

func (s *BoundedPlayerSession) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *BoundedPlayerSession) Close(reason CloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true
	s.closeReason = reason
	close(s.outbound)
}

func (s *BoundedPlayerSession) CloseReason() CloseReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeReason
}

func (s *BoundedPlayerSession) Outbound() <-chan RoomMessage {
	return s.outbound
}

func (s *BoundedPlayerSession) QueueDepth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.outbound)
}

func (s *BoundedPlayerSession) QueueCapacity() int {
	return cap(s.outbound)
}
