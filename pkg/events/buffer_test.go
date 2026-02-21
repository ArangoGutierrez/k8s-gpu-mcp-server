// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEvent(ts time.Time, podName, namespace, reason string) K8sEvent {
	return K8sEvent{
		Timestamp: ts,
		Type:      "Warning",
		Reason:    reason,
		Message:   fmt.Sprintf("test event: %s", reason),
		PodName:   podName,
		Namespace: namespace,
		NodeName:  "gpu-node-01",
	}
}

func TestNewEventBuffer(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		wantErr  bool
	}{
		{name: "valid capacity", capacity: 100, wantErr: false},
		{name: "capacity of 1", capacity: 1, wantErr: false},
		{name: "zero capacity", capacity: 0, wantErr: true},
		{name: "negative capacity", capacity: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf, err := NewEventBuffer(tt.capacity)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, buf)
			} else {
				require.NoError(t, err)
				require.NotNil(t, buf)
				assert.Equal(t, tt.capacity, buf.Capacity())
				assert.Equal(t, 0, buf.Size())
			}
		})
	}
}

func TestEventBuffer_AddAndRetrieve(t *testing.T) {
	buf, err := NewEventBuffer(10)
	require.NoError(t, err)

	now := time.Now()
	event := newTestEvent(now, "my-pod", "default", ReasonFailed)
	buf.Add(event)

	assert.Equal(t, 1, buf.Size())

	all := buf.GetAll()
	require.Len(t, all, 1)
	assert.Equal(t, "my-pod", all[0].PodName)
	assert.Equal(t, ReasonFailed, all[0].Reason)
}

func TestEventBuffer_MultipleEvents_Ordering(t *testing.T) {
	buf, err := NewEventBuffer(10)
	require.NoError(t, err)

	base := time.Now()
	for i := 0; i < 5; i++ {
		buf.Add(newTestEvent(
			base.Add(time.Duration(i)*time.Second),
			fmt.Sprintf("pod-%d", i),
			"default",
			ReasonFailed,
		))
	}

	assert.Equal(t, 5, buf.Size())

	all := buf.GetAll()
	require.Len(t, all, 5)

	// Verify chronological ordering (oldest first)
	for i := 1; i < len(all); i++ {
		assert.True(t, all[i].Timestamp.After(all[i-1].Timestamp) ||
			all[i].Timestamp.Equal(all[i-1].Timestamp),
			"events should be in chronological order")
	}
}

func TestEventBuffer_GetSince(t *testing.T) {
	buf, err := NewEventBuffer(10)
	require.NoError(t, err)

	base := time.Now()
	for i := 0; i < 5; i++ {
		buf.Add(newTestEvent(
			base.Add(time.Duration(i)*time.Second),
			fmt.Sprintf("pod-%d", i),
			"default",
			ReasonFailed,
		))
	}

	t.Run("since before all events", func(t *testing.T) {
		events := buf.GetSince(base.Add(-time.Hour))
		assert.Len(t, events, 5)
	})

	t.Run("since after all events", func(t *testing.T) {
		events := buf.GetSince(base.Add(time.Hour))
		assert.Empty(t, events)
	})

	t.Run("since middle timestamp", func(t *testing.T) {
		events := buf.GetSince(base.Add(2 * time.Second))
		assert.GreaterOrEqual(t, len(events), 3) // events at t+2s, t+3s, t+4s
	})

	t.Run("since exact timestamp", func(t *testing.T) {
		events := buf.GetSince(base)
		assert.GreaterOrEqual(t, len(events), 1)
	})
}

func TestEventBuffer_GetForPod(t *testing.T) {
	buf, err := NewEventBuffer(10)
	require.NoError(t, err)

	base := time.Now()
	buf.Add(newTestEvent(base, "pod-a", "ns-1", ReasonFailed))
	buf.Add(newTestEvent(base.Add(time.Second), "pod-b", "ns-1", ReasonOOMKilled))
	buf.Add(newTestEvent(base.Add(2*time.Second), "pod-a", "ns-1", ReasonBackOff))
	buf.Add(newTestEvent(base.Add(3*time.Second), "pod-a", "ns-2", ReasonFailed))

	t.Run("matching pod and namespace", func(t *testing.T) {
		events := buf.GetForPod("pod-a", "ns-1")
		assert.Len(t, events, 2)
		for _, e := range events {
			assert.Equal(t, "pod-a", e.PodName)
			assert.Equal(t, "ns-1", e.Namespace)
		}
	})

	t.Run("different namespace", func(t *testing.T) {
		events := buf.GetForPod("pod-a", "ns-2")
		assert.Len(t, events, 1)
	})

	t.Run("non-matching pod", func(t *testing.T) {
		events := buf.GetForPod("pod-c", "ns-1")
		assert.Empty(t, events)
	})

	t.Run("non-matching namespace", func(t *testing.T) {
		events := buf.GetForPod("pod-a", "ns-3")
		assert.Empty(t, events)
	})
}

func TestEventBuffer_GetByReason_Filtering(t *testing.T) {
	buf, err := NewEventBuffer(10)
	require.NoError(t, err)

	base := time.Now()
	buf.Add(newTestEvent(base, "pod-a", "default", ReasonFailed))
	buf.Add(newTestEvent(base.Add(time.Second), "pod-b", "default", ReasonOOMKilled))
	buf.Add(newTestEvent(base.Add(2*time.Second), "pod-c", "default", ReasonFailed))
	buf.Add(newTestEvent(base.Add(3*time.Second), "pod-d", "default", ReasonEvicted))

	t.Run("matching reason", func(t *testing.T) {
		events := buf.GetByReason(ReasonFailed)
		assert.Len(t, events, 2)
		for _, e := range events {
			assert.Equal(t, ReasonFailed, e.Reason)
		}
	})

	t.Run("single match", func(t *testing.T) {
		events := buf.GetByReason(ReasonOOMKilled)
		assert.Len(t, events, 1)
	})

	t.Run("non-matching reason", func(t *testing.T) {
		events := buf.GetByReason(ReasonPreempting)
		assert.Empty(t, events)
	})
}

func TestEventBuffer_CapacityOverflow(t *testing.T) {
	capacity := 3
	buf, err := NewEventBuffer(capacity)
	require.NoError(t, err)

	base := time.Now()
	// Add more events than capacity
	for i := 0; i < 5; i++ {
		buf.Add(newTestEvent(
			base.Add(time.Duration(i)*time.Second),
			fmt.Sprintf("pod-%d", i),
			"default",
			ReasonFailed,
		))
	}

	assert.Equal(t, capacity, buf.Size())

	all := buf.GetAll()
	require.Len(t, all, capacity)

	// Oldest events should have been evicted; newest should remain
	assert.Equal(t, "pod-2", all[0].PodName)
	assert.Equal(t, "pod-3", all[1].PodName)
	assert.Equal(t, "pod-4", all[2].PodName)
}

func TestEventBuffer_Latest(t *testing.T) {
	t.Run("empty buffer", func(t *testing.T) {
		buf, err := NewEventBuffer(10)
		require.NoError(t, err)

		_, ok := buf.Latest()
		assert.False(t, ok)
	})

	t.Run("single event", func(t *testing.T) {
		buf, err := NewEventBuffer(10)
		require.NoError(t, err)

		event := newTestEvent(time.Now(), "pod-a", "default", ReasonFailed)
		buf.Add(event)

		latest, ok := buf.Latest()
		assert.True(t, ok)
		assert.Equal(t, "pod-a", latest.PodName)
	})

	t.Run("multiple events returns most recent", func(t *testing.T) {
		buf, err := NewEventBuffer(10)
		require.NoError(t, err)

		base := time.Now()
		buf.Add(newTestEvent(base, "pod-old", "default", ReasonFailed))
		buf.Add(newTestEvent(base.Add(time.Second), "pod-new", "default", ReasonFailed))

		latest, ok := buf.Latest()
		assert.True(t, ok)
		assert.Equal(t, "pod-new", latest.PodName)
	})
}

func TestEventBuffer_Clear(t *testing.T) {
	buf, err := NewEventBuffer(10)
	require.NoError(t, err)

	buf.Add(newTestEvent(time.Now(), "pod-a", "default", ReasonFailed))
	buf.Add(newTestEvent(time.Now(), "pod-b", "default", ReasonFailed))
	assert.Equal(t, 2, buf.Size())

	buf.Clear()
	assert.Equal(t, 0, buf.Size())
	assert.Nil(t, buf.GetAll())
}

func TestEventBuffer_EmptyBufferQueries(t *testing.T) {
	buf, err := NewEventBuffer(10)
	require.NoError(t, err)

	assert.Equal(t, 0, buf.Size())
	assert.Nil(t, buf.GetAll())
	assert.Empty(t, buf.GetSince(time.Now().Add(-time.Hour)))
	assert.Empty(t, buf.GetForPod("pod-a", "default"))
	assert.Empty(t, buf.GetByReason(ReasonFailed))

	_, ok := buf.Latest()
	assert.False(t, ok)
}

func TestEventBuffer_ConcurrentAdd(t *testing.T) {
	buf, err := NewEventBuffer(100)
	require.NoError(t, err)

	const goroutines = 10
	const eventsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				buf.Add(newTestEvent(
					time.Now(),
					fmt.Sprintf("pod-g%d-e%d", id, i),
					"default",
					ReasonFailed,
				))
			}
		}(g)
	}

	wg.Wait()

	// All events should be present (100 capacity >= 200 total, so some eviction)
	assert.Equal(t, 100, buf.Size())
}

func TestEventBuffer_ConcurrentReadWrite(t *testing.T) {
	buf, err := NewEventBuffer(50)
	require.NoError(t, err)

	const writers = 5
	const readers = 5
	const ops = 20

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writers
	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				buf.Add(newTestEvent(
					time.Now(),
					fmt.Sprintf("pod-%d-%d", id, i),
					"default",
					ReasonFailed,
				))
			}
		}(w)
	}

	// Readers
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				_ = buf.GetAll()
				_ = buf.GetSince(time.Now().Add(-time.Hour))
				_ = buf.GetForPod("pod-0-0", "default")
				_ = buf.GetByReason(ReasonFailed)
				_, _ = buf.Latest()
				_ = buf.Size()
			}
		}()
	}

	wg.Wait()

	// No panics or races = success
	assert.LessOrEqual(t, buf.Size(), buf.Capacity())
}
