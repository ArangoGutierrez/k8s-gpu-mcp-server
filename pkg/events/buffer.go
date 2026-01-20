// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
)

// EventBuffer wraps a RingBuffer[K8sEvent] with domain-specific queries.
type EventBuffer struct {
	ring *blackbox.RingBuffer[K8sEvent]
}

// NewEventBuffer creates a new event buffer with the given capacity.
// Returns an error if capacity <= 0.
func NewEventBuffer(capacity int) (*EventBuffer, error) {
	ring, err := blackbox.NewRingBuffer[K8sEvent](capacity)
	if err != nil {
		return nil, err
	}
	return &EventBuffer{ring: ring}, nil
}

// Add inserts an event into the buffer.
// If the buffer is full, the oldest event is evicted.
func (b *EventBuffer) Add(event K8sEvent) {
	b.ring.Add(event)
}

// GetSince returns events newer than or equal to the given timestamp.
// Events are returned in chronological order (oldest first).
func (b *EventBuffer) GetSince(since time.Time) []K8sEvent {
	return b.ring.Query(since, func(e K8sEvent) time.Time {
		return e.Timestamp
	})
}

// GetForPod returns all events for the specified pod.
// Events are returned in chronological order (oldest first).
func (b *EventBuffer) GetForPod(podName, namespace string) []K8sEvent {
	return b.ring.QueryFunc(func(e K8sEvent) bool {
		return e.PodName == podName && e.Namespace == namespace
	})
}

// GetByReason returns all events with the specified reason.
// Events are returned in chronological order (oldest first).
func (b *EventBuffer) GetByReason(reason string) []K8sEvent {
	return b.ring.QueryFunc(func(e K8sEvent) bool {
		return e.Reason == reason
	})
}

// GetAll returns all events in chronological order (oldest first).
// Returns nil if the buffer is empty.
func (b *EventBuffer) GetAll() []K8sEvent {
	return b.ring.All()
}

// Latest returns the most recent event and true, or zero value and false
// if the buffer is empty.
func (b *EventBuffer) Latest() (K8sEvent, bool) {
	return b.ring.Latest()
}

// Size returns the current number of events in the buffer.
func (b *EventBuffer) Size() int {
	return b.ring.Size()
}

// Capacity returns the maximum buffer capacity.
func (b *EventBuffer) Capacity() int {
	return b.ring.Capacity()
}

// Clear removes all events from the buffer.
func (b *EventBuffer) Clear() {
	b.ring.Clear()
}
