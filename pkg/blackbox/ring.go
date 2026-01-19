// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package blackbox

import (
	"errors"
	"sync"
	"time"
)

// Timestamped is an interface for types that have a timestamp.
// Used by RingBuffer.Query to filter items by time.
type Timestamped interface {
	GetTimestamp() time.Time
}

// ErrInvalidCapacity is returned when creating a RingBuffer with capacity <= 0.
var ErrInvalidCapacity = errors.New("ring buffer capacity must be > 0")

// RingBuffer is a generic, thread-safe circular buffer. When the buffer is
// full, the oldest items are evicted to make room for new ones. All
// operations are safe for concurrent use.
type RingBuffer[T any] struct {
	mu       sync.RWMutex
	data     []T
	head     int  // Next write position
	size     int  // Current number of items
	capacity int  // Maximum capacity
	full     bool // True when buffer has wrapped
}

// NewRingBuffer creates a new ring buffer with the specified capacity.
// Returns ErrInvalidCapacity if capacity <= 0.
func NewRingBuffer[T any](capacity int) (*RingBuffer[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &RingBuffer[T]{
		data:     make([]T, capacity),
		capacity: capacity,
	}, nil
}

// Add inserts an item into the buffer.
// If the buffer is full, the oldest item is evicted.
func (r *RingBuffer[T]) Add(item T) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.data[r.head] = item
	r.head = (r.head + 1) % r.capacity

	if r.full {
		// Buffer was already full, size stays at capacity
		return
	}

	r.size++
	if r.size == r.capacity {
		r.full = true
	}
}

// Latest returns the most recently added item and true, or the zero value
// and false if the buffer is empty.
func (r *RingBuffer[T]) Latest() (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var zero T
	if r.size == 0 {
		return zero, false
	}

	// Head points to next write position, so latest is at head-1
	idx := (r.head - 1 + r.capacity) % r.capacity
	return r.data[idx], true
}

// Size returns the current number of items in the buffer.
func (r *RingBuffer[T]) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

// Capacity returns the maximum capacity of the buffer.
func (r *RingBuffer[T]) Capacity() int {
	return r.capacity
}

// All returns all items in the buffer in chronological order (oldest first).
// Returns nil if the buffer is empty.
func (r *RingBuffer[T]) All() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil
	}

	result := make([]T, r.size)
	if r.full {
		// Buffer has wrapped: oldest is at head (which is also next write pos)
		// Copy from head to end, then from start to head
		n := copy(result, r.data[r.head:])
		copy(result[n:], r.data[:r.head])
	} else {
		// Buffer hasn't wrapped: items are 0..size-1
		copy(result, r.data[:r.size])
	}

	return result
}

// Query returns items newer than the given timestamp in chronological order.
// The timeFn extracts the timestamp from an item.
// Returns nil if no items match or the buffer is empty.
func (r *RingBuffer[T]) Query(since time.Time, timeFn func(T) time.Time) []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil
	}

	// Get all items in order first
	var ordered []T
	if r.full {
		ordered = make([]T, r.size)
		n := copy(ordered, r.data[r.head:])
		copy(ordered[n:], r.data[:r.head])
	} else {
		ordered = r.data[:r.size]
	}

	// Find first item >= since using linear scan (items are time-ordered)
	startIdx := -1
	for i, item := range ordered {
		if !timeFn(item).Before(since) {
			startIdx = i
			break
		}
	}

	if startIdx < 0 {
		return nil
	}

	// Copy matching items
	result := make([]T, len(ordered)-startIdx)
	copy(result, ordered[startIdx:])
	return result
}

// QueryFunc returns items that satisfy the predicate in chronological order.
// Returns nil if no items match or the buffer is empty.
func (r *RingBuffer[T]) QueryFunc(pred func(T) bool) []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil
	}

	var result []T

	// Iterate in chronological order
	if r.full {
		// Start from head (oldest) and wrap around
		for i := 0; i < r.size; i++ {
			idx := (r.head + i) % r.capacity
			if pred(r.data[idx]) {
				result = append(result, r.data[idx])
			}
		}
	} else {
		// Items are 0..size-1
		for i := 0; i < r.size; i++ {
			if pred(r.data[i]) {
				result = append(result, r.data[i])
			}
		}
	}

	return result
}

// FindNearest returns the item with timestamp closest to the given time.
// Returns the zero value and false if the buffer is empty.
func (r *RingBuffer[T]) FindNearest(
	target time.Time,
	timeFn func(T) time.Time,
) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var zero T
	if r.size == 0 {
		return zero, false
	}

	var best T
	var bestDiff time.Duration = -1

	// Iterate through all items
	iterFn := func(item T) {
		diff := timeFn(item).Sub(target)
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			best = item
			bestDiff = diff
		}
	}

	if r.full {
		for i := 0; i < r.size; i++ {
			idx := (r.head + i) % r.capacity
			iterFn(r.data[idx])
		}
	} else {
		for i := 0; i < r.size; i++ {
			iterFn(r.data[i])
		}
	}

	return best, true
}

// Clear removes all items from the buffer.
func (r *RingBuffer[T]) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Zero out the slice to allow GC of old items
	var zero T
	for i := range r.data {
		r.data[i] = zero
	}

	r.head = 0
	r.size = 0
	r.full = false
}
