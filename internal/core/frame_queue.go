package core

import (
	"sync"
	"time"
)

// streamDrainTimeout bounds how long the emitter waits for the downstream
// consumer to acknowledge the last frame before the stream is torn down.
const streamDrainTimeout = 30 * time.Second

type queuedFrame struct {
	payload []byte
	final   bool
}

// frameQueue decouples the CLI reader from the downstream emit. The CLI
// abandons a generation whose stdout consumer stalls, so the reader must
// never block on the host round trip.
type frameQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []queuedFrame
	head   int
	closed bool
}

func newFrameQueue() *frameQueue {
	q := &frameQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push never blocks the producer.
func (q *frameQueue) push(payload []byte, final bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, queuedFrame{payload: payload, final: final})
	q.cond.Signal()
}

func (q *frameQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

// next blocks until a frame is available or the queue is closed and drained.
func (q *frameQueue) next() (queuedFrame, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.head == len(q.items) && !q.closed {
		q.cond.Wait()
	}
	if q.head == len(q.items) {
		return queuedFrame{}, false
	}
	frame := q.items[q.head]
	q.items[q.head] = queuedFrame{}
	q.head++
	if q.head > 64 && q.head*2 >= len(q.items) {
		q.items = append(q.items[:0], q.items[q.head:]...)
		q.head = 0
	}
	return frame, true
}
