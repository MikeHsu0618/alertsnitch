package loki

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// queuedAlert is one alert group awaiting batched delivery, together with the
// per-request labels captured when it was enqueued.
type queuedAlert struct {
	group       *internal.AlertGroup
	extraLabels map[string]string
}

// convertedGroup pairs a queued alert with its successfully built streams so
// the flusher can account for each group independently.
type convertedGroup struct {
	qa      queuedAlert
	streams []stream
}

// batchProcessor decouples three concerns that the original implementation
// fused into one goroutine:
//   - accumulation: the consumer drains the inbound channel into batches
//   - delivery: a dedicated flusher ships batches with retries, so retry
//     backoff never blocks accumulation (no head-of-line blocking)
//   - accounting: every alert is recorded as saved or failed at the real point
//     of delivery — including alerts dropped because the queue was full
type batchProcessor struct {
	client *Client
	cfg    BatchConfig

	in       chan queuedAlert
	flushCh  chan []queuedAlert
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	// runCtx bounds all delivery work; canceling it aborts in-flight pushes
	// and pending retries so shutdown does not run on past its deadline.
	runCtx    context.Context
	runCancel context.CancelFunc
}

func newBatchProcessor(client *Client, cfg BatchConfig) *batchProcessor {
	bufferSize := cfg.Size * 10
	if bufferSize < 1000 {
		bufferSize = 1000
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	return &batchProcessor{
		client:    client,
		cfg:       cfg,
		in:        make(chan queuedAlert, bufferSize),
		flushCh:   make(chan []queuedAlert, 4),
		stopCh:    make(chan struct{}),
		runCtx:    runCtx,
		runCancel: runCancel,
	}
}

func (b *batchProcessor) start() {
	b.wg.Add(2)
	go b.accumulate()
	go b.flusher()
}

// enqueue offers an alert to the queue, applying brief backpressure before
// giving up. A dropped alert is recorded as a saving failure so the loss is
// observable in metrics rather than silent.
func (b *batchProcessor) enqueue(ctx context.Context, qa queuedAlert) error {
	select {
	case b.in <- qa:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case b.in <- qa:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		logrus.Warn("Loki alert queue is full, dropping alert")
		recordOutcome(qa.group.Receiver, qa.group.Status, len(qa.group.Alerts), errQueueFull)
		return errQueueFull
	}
}

// accumulate drains the inbound channel into size/time-bounded batches and
// hands each to the flusher. It never performs network I/O itself.
func (b *batchProcessor) accumulate() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.cfg.FlushTimeout)
	defer ticker.Stop()

	batch := make([]queuedAlert, 0, b.cfg.Size)
	dispatch := func() {
		if len(batch) == 0 {
			return
		}
		b.flushCh <- batch
		batch = make([]queuedAlert, 0, b.cfg.Size)
	}

	for {
		select {
		case <-b.stopCh:
			// Drain anything already queued, then flush the remainder.
			for {
				select {
				case qa := <-b.in:
					batch = append(batch, qa)
				default:
					dispatch()
					close(b.flushCh)
					return
				}
			}
		case qa := <-b.in:
			batch = append(batch, qa)
			if len(batch) >= b.cfg.Size {
				dispatch()
			}
		case <-ticker.C:
			dispatch()
		}
	}
}

func (b *batchProcessor) flusher() {
	defer b.wg.Done()
	for batch := range b.flushCh {
		b.flush(batch)
	}
}

func (b *batchProcessor) flush(batch []queuedAlert) {
	if len(batch) == 0 {
		return
	}

	// Convert each group independently. A group that fails stream conversion is
	// its own failure — it must not be silently skipped, nor borrow another
	// group's (possibly successful) delivery outcome.
	ready := make([]convertedGroup, 0, len(batch))
	for _, qa := range batch {
		streams, err := b.client.dataToStream(qa.group, qa.extraLabels)
		if err != nil {
			logrus.Errorf("Error converting data to stream: %v", err)
			recordOutcome(qa.group.Receiver, qa.group.Status, len(qa.group.Alerts), err)
			continue
		}
		ready = append(ready, convertedGroup{qa: qa, streams: streams})
	}
	if len(ready) == 0 {
		return
	}

	err := b.deliver(mergeStreams(ready))
	for _, g := range ready {
		recordOutcome(g.qa.group.Receiver, g.qa.group.Status, len(g.qa.group.Alerts), err)
	}
}

// deliver pushes merged streams with bounded retries. It runs on the flusher
// goroutine, so its backoff sleeps do not stall accumulation, and it honors
// runCtx so a shutdown deadline aborts in-flight work instead of running on.
func (b *batchProcessor) deliver(streams []stream) error {
	if len(streams) == 0 {
		return nil
	}

	p := payload{Streams: streams}
	var lastErr error
	for attempt := 0; attempt <= b.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(b.cfg.RetryDelay * time.Duration(attempt)):
			case <-b.runCtx.Done():
				return fmt.Errorf("loki batch flush aborted during shutdown: %w", b.runCtx.Err())
			}
			logrus.Warnf("Retrying loki batch flush, attempt %d/%d", attempt, b.cfg.MaxRetries)
		}

		ctx, cancel := context.WithTimeout(b.runCtx, b.client.cfg.RequestTimeout)
		err := b.client.pushPayload(ctx, p)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		logrus.Errorf("Failed to flush loki batch (attempt %d/%d): %v", attempt+1, b.cfg.MaxRetries+1, err)
		if b.runCtx.Err() != nil {
			return fmt.Errorf("loki batch flush aborted during shutdown: %w", b.runCtx.Err())
		}
	}
	logrus.Errorf("Giving up on loki batch after %d attempts: %v", b.cfg.MaxRetries+1, lastErr)
	return lastErr
}

func mergeStreams(groups []convertedGroup) []stream {
	streamMap := make(map[string]*stream)
	for _, g := range groups {
		for _, s := range g.streams {
			key := streamKey(s.Stream)
			if existing, ok := streamMap[key]; ok {
				existing.Values = append(existing.Values, s.Values...)
				continue
			}
			cp := stream{Stream: s.Stream, Values: make([]row, len(s.Values))}
			copy(cp.Values, s.Values)
			streamMap[key] = &cp
		}
	}

	result := make([]stream, 0, len(streamMap))
	for _, s := range streamMap {
		result = append(result, *s)
	}
	return result
}

// stop signals shutdown and waits for buffered alerts to flush, bounded by ctx.
// On a clean drain it returns nil; if ctx expires first it aborts in-flight
// delivery and returns an error so the caller knows the drain was incomplete
// and some buffered alerts may have been lost. Safe to call more than once.
func (b *batchProcessor) stop(ctx context.Context) error {
	b.stopOnce.Do(func() { close(b.stopCh) })

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.runCancel()
		return nil
	case <-ctx.Done():
		// Abort in-flight pushes/retries so delivery does not continue past the
		// deadline on a background context.
		b.runCancel()
		err := fmt.Errorf("loki batch shutdown did not complete within the deadline: %w", ctx.Err())
		logrus.Warnf("%v; some buffered alerts may be lost", err)
		return err
	}
}
