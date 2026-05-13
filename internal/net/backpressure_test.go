package netx

import (
	"errors"
	"testing"
)

func TestSendQueueIsBounded(t *testing.T) {
	queue, err := NewSendQueue[int](1)
	if err != nil {
		t.Fatalf("NewSendQueue returned error: %v", err)
	}

	if err := queue.TryEnqueue(1); err != nil {
		t.Fatalf("first enqueue returned error: %v", err)
	}
	if err := queue.TryEnqueue(2); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("second enqueue error = %v, want ErrQueueFull", err)
	}
}
