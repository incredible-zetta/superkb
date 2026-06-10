package usecase

import (
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"superkb/internal/domain"
)

// BuildProcessor processes a single enqueued build. Implemented by
// KnowledgeBaseUseCase.ProcessBuild.
type BuildProcessor interface {
	ProcessBuild(ctx context.Context, buildID uuid.UUID) error
}

// ChannelBuildQueue is an in-process domain.BuildQueue backed by a buffered
// channel. It is the default queue and also drives BuildWorker. Swap it for a
// durable queue (Redis, SQS) by implementing domain.BuildQueue elsewhere.
type ChannelBuildQueue struct {
	ch chan uuid.UUID
}

// NewChannelBuildQueue creates a queue with the given buffer size.
func NewChannelBuildQueue(size int) *ChannelBuildQueue {
	if size <= 0 {
		size = 64
	}
	return &ChannelBuildQueue{ch: make(chan uuid.UUID, size)}
}

// Enqueue schedules a build id. Returns ctx error if the context is done while
// the buffer is full.
func (q *ChannelBuildQueue) Enqueue(ctx context.Context, buildID uuid.UUID) error {
	select {
	case q.ch <- buildID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BuildWorker drains a ChannelBuildQueue and processes builds concurrently.
type BuildWorker struct {
	queue       *ChannelBuildQueue
	processor   BuildProcessor
	concurrency int
	logger      *slog.Logger
	wg          sync.WaitGroup
}

// NewBuildWorker wires a worker. concurrency < 1 defaults to 1.
func NewBuildWorker(queue *ChannelBuildQueue, processor BuildProcessor, concurrency int, logger *slog.Logger) *BuildWorker {
	if concurrency < 1 {
		concurrency = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BuildWorker{queue: queue, processor: processor, concurrency: concurrency, logger: logger}
}

// Start launches worker goroutines. They stop when ctx is cancelled or the
// queue channel is closed. Call Wait to block until all goroutines exit.
func (w *BuildWorker) Start(ctx context.Context) {
	for i := 0; i < w.concurrency; i++ {
		w.wg.Add(1)
		go w.loop(ctx)
	}
}

func (w *BuildWorker) loop(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case buildID, ok := <-w.queue.ch:
			if !ok {
				return
			}
			// Use a detached context so a build in flight is not aborted by a
			// transient request context; it is bounded by the worker shutdown.
			if err := w.processor.ProcessBuild(context.WithoutCancel(ctx), buildID); err != nil {
				w.logger.Error("build processing failed", "build_id", buildID, "error", err)
			} else {
				w.logger.Info("build processed", "build_id", buildID)
			}
		}
	}
}

// Wait blocks until all worker goroutines have exited.
func (w *BuildWorker) Wait() { w.wg.Wait() }

// compile-time assertions.
var (
	_ domain.BuildQueue = (*ChannelBuildQueue)(nil)
	_ BuildProcessor    = (*KnowledgeBaseUseCase)(nil)
)
