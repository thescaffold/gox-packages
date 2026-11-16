package ws

import (
	"context"
	"sync"
)

// Adapter is the pub/sub transport that carries a hub's broadcasts between
// nodes: every hub on the same channel delivers what any of them emits. The
// in-process MemoryAdapter is the reference implementation; a Redis or NATS
// adapter implements the same two methods.
//
// Delivery is at-most-once and best-effort, like the sockets themselves:
// a client that must not miss anything resyncs from the database on
// reconnect (durable delivery is core/events' job, not the socket's).
type Adapter interface {
	// Publish sends data to every subscriber of channel, this node's included.
	Publish(ctx context.Context, channel string, data []byte) error
	// Subscribe calls handler with every message published on channel until
	// unsubscribe is called. A handler must not block.
	Subscribe(ctx context.Context, channel string, handler func(data []byte)) (unsubscribe func(), err error)
}

// MemoryAdapter is an Adapter inside one process: hubs that share one act as
// the nodes of a cluster (tests, or several hubs in one binary).
type MemoryAdapter struct {
	mu   sync.RWMutex
	next int
	subs map[string]map[int]func([]byte)
}

// NewMemoryAdapter returns an empty MemoryAdapter.
func NewMemoryAdapter() *MemoryAdapter {
	return &MemoryAdapter{subs: map[string]map[int]func([]byte){}}
}

// Publish calls every subscriber of channel, in subscription order, before it
// returns.
func (m *MemoryAdapter) Publish(_ context.Context, channel string, data []byte) error {
	m.mu.RLock()
	set := m.subs[channel]
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	handlers := make([]func([]byte), 0, len(ids))
	sortInts(ids)
	for _, id := range ids {
		handlers = append(handlers, set[id])
	}
	m.mu.RUnlock()
	for _, h := range handlers {
		h(append([]byte(nil), data...))
	}
	return nil
}

// Subscribe registers handler on channel.
func (m *MemoryAdapter) Subscribe(_ context.Context, channel string, handler func([]byte)) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subs == nil {
		m.subs = map[string]map[int]func([]byte){}
	}
	if m.subs[channel] == nil {
		m.subs[channel] = map[int]func([]byte){}
	}
	m.next++
	id := m.next
	m.subs[channel][id] = handler
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subs[channel], id)
			m.mu.Unlock()
		})
	}, nil
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
