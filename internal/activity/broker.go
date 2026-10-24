// Package activity provides a process-local fan-out broker that delivers
// indexed activity events to live Server-Sent Events subscribers. It keeps the
// REST API decoupled from the indexer: the indexer publishes, the SSE handler
// subscribes, and neither knows about the other.
package activity

import (
	"encoding/json"
	"sync"
	"time"
)

// Event is a single activity occurrence, published by the indexer as soon as
// it records the corresponding row. It mirrors store.Activity so clients can
// treat streamed events identically to the rows returned by GET /api/activity.
type Event struct {
	Ledger   int64           `json:"ledger"`
	TxHash   string          `json:"tx_hash"`
	Contract string          `json:"contract"`
	Event    string          `json:"event"`
	Payload  json.RawMessage `json:"payload"`
	SeenAt   time.Time       `json:"seen_at"`
}

// Broker fans out Event values to all current subscribers. It is safe for
// concurrent use by multiple publishers and subscribers.
//
// Each subscriber gets a buffered channel. Publishing never blocks: if a
// subscriber's buffer is full, its oldest buffered event is dropped so the
// newest activity always wins. A subscriber that forgets to cancel leaves a
// bounded buffer behind, so memory stays finite even if a disconnect is missed.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
	bufSize     int
}

// NewBroker returns a broker that keeps bufSize pending events per subscriber.
// bufSize is clamped to a minimum of 1.
func NewBroker(bufSize int) *Broker {
	if bufSize < 1 {
		bufSize = 1
	}
	return &Broker{
		subscribers: make(map[chan Event]struct{}),
		bufSize:     bufSize,
	}
}

// Subscribe registers a new subscriber and returns a receive-only channel of
// events plus a cancel function. The caller MUST call cancel when the client
// disconnects (for example from r.Context().Done()) so the subscription is
// reclaimed.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, b.bufSize)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subscribers[ch]; !ok {
			return
		}
		delete(b.subscribers, ch)
		close(ch)
	}
	return ch, cancel
}

// Publish delivers ev to every subscriber. It returns once all subscribers
// have been offered the event. Slow subscribers drop their oldest buffered
// event rather than stalling the publisher, so the buffer always holds the
// freshest activity.
func (b *Broker) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- ev:
			// delivered
		default:
			// Subscriber is behind: evict the oldest buffered event and retry
			// once so the newest activity is kept instead of stalling.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
}
