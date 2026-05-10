package events

import "sync"

// Queue is an in-memory FIFO of Items. Mirrors jsx-polylog/common/queue/index.ts.
// Pushed items inherit the SourceID/EntityName when not pre-set.
type Queue struct {
	mu       sync.Mutex
	store    []Item
	sourceID string
}

// NewQueue creates an empty queue tagged with the given sourceID.
func NewQueue(sourceID string) *Queue {
	return &Queue{sourceID: sourceID}
}

// Push appends items to the end of the queue. Each item that does not already
// have an EntityID inherits sourceID and EventEntitySource as its EntityName,
// matching jsx-polylog push() defaults.
func (q *Queue) Push(items ...Item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, it := range items {
		if it.EntityID == "" {
			it.EntityID = q.sourceID
		}
		if it.EntityName == "" {
			it.EntityName = EventEntitySource
		}
		q.store = append(q.store, it)
	}
}

// Pop removes up to n items from the front of the queue and returns them.
func (q *Queue) Pop(n int) []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	if n <= 0 || len(q.store) == 0 {
		return nil
	}
	if n > len(q.store) {
		n = len(q.store)
	}
	out := make([]Item, n)
	copy(out, q.store[:n])
	q.store = q.store[n:]
	return out
}

// Len returns the current queue length.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.store)
}
