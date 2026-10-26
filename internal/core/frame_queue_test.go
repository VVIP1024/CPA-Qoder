package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFrameQueueNeverBlocksProducer(t *testing.T) {
	q := newFrameQueue()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			q.push([]byte("{}"), false)
		}
		q.close()
	}()
	count := 0
	for {
		frame, ok := q.next()
		if !ok {
			break
		}
		if string(frame.payload) != "{}" {
			t.Fatalf("bad payload %q", frame.payload)
		}
		count++
	}
	wg.Wait()
	if count != 1000 {
		t.Fatalf("drained %d frames", count)
	}
}

func TestFrameQueueCloseDrainsPendingFrames(t *testing.T) {
	q := newFrameQueue()
	q.push([]byte("a"), false)
	q.push([]byte("b"), true)
	q.close()
	q.push([]byte("c"), false)
	var got []string
	for {
		frame, ok := q.next()
		if !ok {
			break
		}
		got = append(got, string(frame.payload))
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("unexpected frames %v", got)
	}
}

func TestFrameQueueNextUnblocksOnClose(t *testing.T) {
	q := newFrameQueue()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, ok := q.next(); ok {
			t.Error("expected no frame")
		}
	}()
	time.Sleep(10 * time.Millisecond)
	q.close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("next did not unblock on close")
	}
}

// A CLI reader that waits on the downstream consumer starves the CLI, which
// then abandons the generation. The queue keeps the reader independent.
func TestQueueKeepsProducerIndependentFromConsumer(t *testing.T) {
	q := newFrameQueue()
	var produced atomic.Int64
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < 50; i++ {
			q.push([]byte("x"), false)
			produced.Add(1)
		}
		q.close()
	}()
	deadline := time.Now().Add(200 * time.Millisecond)
	for produced.Load() < 50 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if produced.Load() < 50 {
		t.Fatalf("producer stalled at %d frames", produced.Load())
	}
	<-finished
}
