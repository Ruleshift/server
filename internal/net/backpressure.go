package netx

import (
	"errors"
	"fmt"
)

var ErrQueueFull = errors.New("bounded send queue is full")

type SendQueue[T any] struct {
	ch chan T
}

func NewSendQueue[T any](capacity int) (*SendQueue[T], error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("send queue capacity must be positive")
	}
	return &SendQueue[T]{ch: make(chan T, capacity)}, nil
}

func (q *SendQueue[T]) TryEnqueue(value T) error {
	select {
	case q.ch <- value:
		return nil
	default:
		return ErrQueueFull
	}
}

func (q *SendQueue[T]) Channel() <-chan T {
	return q.ch
}

func (q *SendQueue[T]) Depth() int {
	return len(q.ch)
}

func (q *SendQueue[T]) Capacity() int {
	return cap(q.ch)
}
