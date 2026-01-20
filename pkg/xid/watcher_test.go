// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatcherConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  WatcherConfig
		wantErr bool
	}{
		{
			name:    "valid default config",
			config:  DefaultWatcherConfig(),
			wantErr: false,
		},
		{
			name:    "valid zero buffer size (uses default)",
			config:  WatcherConfig{BufferSize: 0},
			wantErr: false,
		},
		{
			name:    "valid custom buffer size",
			config:  WatcherConfig{BufferSize: 50},
			wantErr: false,
		},
		{
			name:    "invalid negative buffer size",
			config:  WatcherConfig{BufferSize: -1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWatcherConfig_WithDefaults(t *testing.T) {
	t.Run("applies defaults to zero values", func(t *testing.T) {
		cfg := WatcherConfig{}.WithDefaults()
		assert.Equal(t, 100, cfg.BufferSize)
		assert.Equal(t, DefaultKmsgPath, cfg.KmsgPath)
	})

	t.Run("preserves non-zero values", func(t *testing.T) {
		cfg := WatcherConfig{
			BufferSize: 50,
			KmsgPath:   "/custom/path",
		}.WithDefaults()
		assert.Equal(t, 50, cfg.BufferSize)
		assert.Equal(t, "/custom/path", cfg.KmsgPath)
	})
}

func TestDefaultWatcherConfig(t *testing.T) {
	cfg := DefaultWatcherConfig()
	assert.Equal(t, 100, cfg.BufferSize)
	assert.Equal(t, DefaultKmsgPath, cfg.KmsgPath)
}

func TestNewWatcher(t *testing.T) {
	t.Run("creates watcher with valid config", func(t *testing.T) {
		cfg := WatcherConfig{
			BufferSize: 50,
			KmsgPath:   "/tmp/test-kmsg",
		}
		w, err := NewWatcher(cfg)
		require.NoError(t, err)
		require.NotNil(t, w)
		assert.Equal(t, 50, w.config.BufferSize)
		assert.Equal(t, "/tmp/test-kmsg", w.config.KmsgPath)
		assert.False(t, w.IsRunning())
	})

	t.Run("applies defaults", func(t *testing.T) {
		cfg := WatcherConfig{}
		w, err := NewWatcher(cfg)
		require.NoError(t, err)
		assert.Equal(t, 100, w.config.BufferSize)
		assert.Equal(t, DefaultKmsgPath, w.config.KmsgPath)
	})

	t.Run("rejects invalid config", func(t *testing.T) {
		cfg := WatcherConfig{BufferSize: -1}
		w, err := NewWatcher(cfg)
		assert.Error(t, err)
		assert.Nil(t, w)
		assert.Contains(t, err.Error(), "invalid config")
	})
}

func TestWatcher_StartStop(t *testing.T) {
	// Create a readable temp file to simulate kmsg
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")
	err := os.WriteFile(tmpFile, []byte(""), 0644)
	require.NoError(t, err)

	cfg := WatcherConfig{
		BufferSize: 10,
		KmsgPath:   tmpFile,
	}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	// Initially not running
	assert.False(t, w.IsRunning())

	// Start
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = w.Start(ctx)
	require.NoError(t, err)
	assert.True(t, w.IsRunning())

	// Give watcher time to start
	time.Sleep(50 * time.Millisecond)

	// Stop
	w.Stop()
	assert.False(t, w.IsRunning())
}

func TestWatcher_StartTwice(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")
	err := os.WriteFile(tmpFile, []byte(""), 0644)
	require.NoError(t, err)

	cfg := WatcherConfig{KmsgPath: tmpFile}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	ctx := context.Background()

	// First start succeeds
	err = w.Start(ctx)
	require.NoError(t, err)
	defer w.Stop()

	// Second start returns error
	err = w.Start(ctx)
	assert.ErrorIs(t, err, ErrAlreadyStarted)
}

func TestWatcher_StopTwice(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")
	err := os.WriteFile(tmpFile, []byte(""), 0644)
	require.NoError(t, err)

	cfg := WatcherConfig{KmsgPath: tmpFile}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	err = w.Start(ctx)
	require.NoError(t, err)

	// Stop twice should not panic
	w.Stop()
	w.Stop() // Should be safe
	assert.False(t, w.IsRunning())
}

func TestWatcher_StopWithoutStart(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/nonexistent"}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	// Stop without start should not panic
	w.Stop()
	assert.False(t, w.IsRunning())
}

func TestWatcher_RegisterHandler(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test"}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	var called atomic.Bool
	handler := func(event XIDEvent) {
		called.Store(true)
	}

	w.RegisterHandler(handler)

	// Nil handler should be ignored
	w.RegisterHandler(nil)

	// Should have exactly one handler
	w.mu.RLock()
	assert.Len(t, w.handlers, 1)
	w.mu.RUnlock()
}

func TestWatcher_HandlerPanic(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test"}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	var secondHandlerCalled atomic.Bool

	// Register a panicking handler
	w.RegisterHandler(func(event XIDEvent) {
		panic("test panic")
	})

	// Register a second handler that should still be called
	w.RegisterHandler(func(event XIDEvent) {
		secondHandlerCalled.Store(true)
	})

	// Create a test event
	event := XIDEvent{
		XIDCode:  48,
		PCIBusID: "0000:00:1E.0",
	}

	// This should not panic
	w.notifyHandlers(event)

	// Second handler should have been called
	assert.True(t, secondHandlerCalled.Load())
}

func TestWatcher_GetEvents(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 10}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	// Add some events directly to the buffer
	now := time.Now()
	events := []XIDEvent{
		{Timestamp: now.Add(-5 * time.Minute), XIDCode: 48, PCIBusID: "0000:00:1E.0"},
		{Timestamp: now.Add(-3 * time.Minute), XIDCode: 79, PCIBusID: "0000:00:1F.0"},
		{Timestamp: now.Add(-1 * time.Minute), XIDCode: 48, PCIBusID: "0000:00:1E.0"},
	}
	for _, e := range events {
		w.buffer.Add(e)
	}

	t.Run("get all events", func(t *testing.T) {
		result := w.GetEvents(now.Add(-10 * time.Minute))
		assert.Len(t, result, 3)
	})

	t.Run("get recent events", func(t *testing.T) {
		result := w.GetEvents(now.Add(-2 * time.Minute))
		assert.Len(t, result, 1)
		assert.Equal(t, 48, result[0].XIDCode)
	})

	t.Run("get no events", func(t *testing.T) {
		result := w.GetEvents(now.Add(1 * time.Minute))
		assert.Empty(t, result)
	})
}

func TestWatcher_GetEventsByGPU(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 10}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	// Add events for different GPUs
	now := time.Now()
	events := []XIDEvent{
		{Timestamp: now.Add(-3 * time.Minute), XIDCode: 48, PCIBusID: "0000:00:1E.0"},
		{Timestamp: now.Add(-2 * time.Minute), XIDCode: 79, PCIBusID: "0000:00:1F.0"},
		{Timestamp: now.Add(-1 * time.Minute), XIDCode: 31, PCIBusID: "0000:00:1E.0"},
	}
	for _, e := range events {
		w.buffer.Add(e)
	}

	t.Run("filter by PCI bus ID", func(t *testing.T) {
		result := w.GetEventsByGPU("0000:00:1E.0")
		assert.Len(t, result, 2)
		assert.Equal(t, 48, result[0].XIDCode)
		assert.Equal(t, 31, result[1].XIDCode)
	})

	t.Run("normalized PCI bus ID", func(t *testing.T) {
		// Should normalize the input
		result := w.GetEventsByGPU("00:1E.0")
		assert.Len(t, result, 2)
	})

	t.Run("no matching GPU", func(t *testing.T) {
		result := w.GetEventsByGPU("0000:00:FF.0")
		assert.Empty(t, result)
	})
}

func TestWatcher_GetLatest(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 10}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	t.Run("empty buffer returns nil", func(t *testing.T) {
		result := w.GetLatest()
		assert.Nil(t, result)
	})

	t.Run("returns most recent event", func(t *testing.T) {
		now := time.Now()
		w.buffer.Add(XIDEvent{Timestamp: now.Add(-2 * time.Minute), XIDCode: 48})
		w.buffer.Add(XIDEvent{Timestamp: now.Add(-1 * time.Minute), XIDCode: 79})

		result := w.GetLatest()
		require.NotNil(t, result)
		assert.Equal(t, 79, result.XIDCode)
	})
}

func TestWatcher_EventCount(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 10}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	assert.Equal(t, 0, w.EventCount())

	w.buffer.Add(XIDEvent{XIDCode: 48})
	assert.Equal(t, 1, w.EventCount())

	w.buffer.Add(XIDEvent{XIDCode: 79})
	assert.Equal(t, 2, w.EventCount())
}

func TestWatcher_GetAllEvents(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 10}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	t.Run("empty buffer", func(t *testing.T) {
		result := w.GetAllEvents()
		assert.Nil(t, result)
	})

	t.Run("returns all events in order", func(t *testing.T) {
		now := time.Now()
		w.buffer.Add(XIDEvent{Timestamp: now.Add(-2 * time.Minute), XIDCode: 48})
		w.buffer.Add(XIDEvent{Timestamp: now.Add(-1 * time.Minute), XIDCode: 79})

		result := w.GetAllEvents()
		assert.Len(t, result, 2)
		assert.Equal(t, 48, result[0].XIDCode)
		assert.Equal(t, 79, result[1].XIDCode)
	})
}

func TestWatcher_BufferFull(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 3}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	// Add more events than buffer capacity
	for i := 1; i <= 5; i++ {
		w.buffer.Add(XIDEvent{XIDCode: i})
	}

	// Should only have last 3 events
	assert.Equal(t, 3, w.EventCount())

	events := w.GetAllEvents()
	assert.Len(t, events, 3)
	assert.Equal(t, 3, events[0].XIDCode) // Oldest remaining
	assert.Equal(t, 4, events[1].XIDCode)
	assert.Equal(t, 5, events[2].XIDCode) // Newest
}

func TestWatcher_HandleMessage(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 10}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	var receivedEvents []XIDEvent
	var mu sync.Mutex

	w.RegisterHandler(func(event XIDEvent) {
		mu.Lock()
		receivedEvents = append(receivedEvents, event)
		mu.Unlock()
	})

	// Simulate handling a valid XID message
	w.handleMessage("NVRM: Xid (PCI:0000:00:1E.0): 48, pid='1234', name=python3")

	// Should have added to buffer
	assert.Equal(t, 1, w.EventCount())

	// Should have notified handler
	mu.Lock()
	assert.Len(t, receivedEvents, 1)
	assert.Equal(t, 48, receivedEvents[0].XIDCode)
	assert.Equal(t, "0000:00:1E.0", receivedEvents[0].PCIBusID)
	assert.Equal(t, 1234, receivedEvents[0].PID)
	assert.Equal(t, "python3", receivedEvents[0].ProcessName)
	// Should have enriched severity/description
	assert.Equal(t, "fatal", receivedEvents[0].Severity)
	assert.NotEmpty(t, receivedEvents[0].Description)
	mu.Unlock()
}

func TestWatcher_HandleMessage_InvalidMessage(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 10}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	// Handle invalid message (no XID info)
	w.handleMessage("NVRM: GPU initialized")

	// Should not add to buffer
	assert.Equal(t, 0, w.EventCount())
}

func TestWatcher_ConcurrentAccess(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/tmp/test", BufferSize: 100}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	var wg sync.WaitGroup
	const numGoroutines = 10
	const numOps = 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				w.buffer.Add(XIDEvent{XIDCode: id*1000 + j})
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_ = w.GetAllEvents()
				_ = w.GetLatest()
				_ = w.EventCount()
			}
		}()
	}

	// Concurrent handler registration
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				w.RegisterHandler(func(e XIDEvent) {})
			}
		}()
	}

	wg.Wait()

	// Should not panic and buffer should be valid
	assert.LessOrEqual(t, w.EventCount(), 100)
}

func TestWatcher_StartWithUnavailableKmsg(t *testing.T) {
	cfg := WatcherConfig{KmsgPath: "/nonexistent/kmsg"}
	w, err := NewWatcher(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	err = w.Start(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
	assert.False(t, w.IsRunning())
}

func TestEnrichEvent(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		EnrichEvent(nil) // Should not panic
	})

	t.Run("known XID code", func(t *testing.T) {
		event := &XIDEvent{XIDCode: 48}
		EnrichEvent(event)
		assert.Equal(t, "fatal", event.Severity)
		assert.Contains(t, event.Description, "ECC")
	})

	t.Run("unknown XID code", func(t *testing.T) {
		event := &XIDEvent{XIDCode: 9999}
		EnrichEvent(event)
		assert.Equal(t, "warning", event.Severity)
		assert.Contains(t, event.Description, "not in known error table")
	})
}

func TestXIDEvent_GetTimestamp(t *testing.T) {
	now := time.Now()
	event := XIDEvent{Timestamp: now}
	assert.Equal(t, now, event.GetTimestamp())
}
