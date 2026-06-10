package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// recordingProcessor records processed build ids and signals completion.
type recordingProcessor struct {
	mu        sync.Mutex
	processed []uuid.UUID
	done      chan struct{}
	err       error
}

func (p *recordingProcessor) ProcessBuild(_ context.Context, buildID uuid.UUID) error {
	p.mu.Lock()
	p.processed = append(p.processed, buildID)
	p.mu.Unlock()
	if p.done != nil {
		p.done <- struct{}{}
	}
	return p.err
}

func TestChannelQueue_WorkerProcessesEnqueuedBuilds(t *testing.T) {
	queue := NewChannelBuildQueue(8)
	proc := &recordingProcessor{done: make(chan struct{}, 3)}
	worker := NewBuildWorker(queue, proc, 2, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)

	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids {
		if err := queue.Enqueue(context.Background(), id); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	for range ids {
		select {
		case <-proc.done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for build processing")
		}
	}

	proc.mu.Lock()
	got := len(proc.processed)
	proc.mu.Unlock()
	if got != 3 {
		t.Fatalf("expected 3 processed builds, got %d", got)
	}
}

func TestChannelQueue_ProcessorErrorDoesNotStopWorker(t *testing.T) {
	queue := NewChannelBuildQueue(4)
	proc := &recordingProcessor{done: make(chan struct{}, 2), err: errors.New("boom")}
	worker := NewBuildWorker(queue, proc, 1, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)

	_ = queue.Enqueue(context.Background(), uuid.New())
	_ = queue.Enqueue(context.Background(), uuid.New())

	for i := 0; i < 2; i++ {
		select {
		case <-proc.done:
		case <-time.After(2 * time.Second):
			t.Fatal("worker stopped after error")
		}
	}
}

func TestEnqueue_ContextCancelledWhenFull(t *testing.T) {
	queue := NewChannelBuildQueue(1)
	if err := queue.Enqueue(context.Background(), uuid.New()); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled; buffer is full so Enqueue must observe ctx
	if err := queue.Enqueue(ctx, uuid.New()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
