// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbedded_Interface(t *testing.T) {
	// Verify Embedded implements Interface
	var _ Interface = (*Embedded)(nil)
}

func TestEmbedded_NewEmbedded(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantPort int
	}{
		{
			name:     "default port",
			config:   Config{},
			wantPort: 5555,
		},
		{
			name:     "negative port uses default",
			config:   Config{EmbeddedPort: -1},
			wantPort: 5555,
		},
		{
			name:     "zero port uses default",
			config:   Config{EmbeddedPort: 0},
			wantPort: 5555,
		},
		{
			name:     "custom port",
			config:   Config{EmbeddedPort: 6666},
			wantPort: 6666,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewEmbedded(tt.config, nil)
			if e == nil {
				t.Fatal("NewEmbedded returned nil")
			}
			if e.hostenginePort != tt.wantPort {
				t.Errorf("hostenginePort = %d, want %d", e.hostenginePort, tt.wantPort)
			}
		})
	}
}

func TestEmbedded_Init_HostengineNotFound(t *testing.T) {
	// This test verifies that Init returns ErrHostengineNotFound when
	// nv-hostengine is not in PATH. This is the expected behavior in
	// most test environments.
	e := NewEmbedded(DefaultConfig(), nil)

	err := e.Init(context.Background())

	// In test environments, nv-hostengine is typically not available
	if err != nil && !errors.Is(err, ErrHostengineNotFound) {
		t.Errorf("Init() error = %v, want ErrHostengineNotFound", err)
	}
}

func TestEmbedded_Init_AlreadyInitialized(t *testing.T) {
	e := NewEmbedded(DefaultConfig(), nil)

	// Manually set initialized to true to simulate already initialized state
	e.initialized.Store(true)

	err := e.Init(context.Background())
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Errorf("Init() error = %v, want ErrAlreadyInitialized", err)
	}
}

func TestEmbedded_Init_Concurrent(t *testing.T) {
	// This test verifies that concurrent Init() calls are handled correctly.
	// Only one should succeed (or return hostengine not found), and the rest
	// should return ErrAlreadyInitialized.
	//
	// This test validates the fix for the race condition where the initialized
	// check was performed outside the lock.

	const numGoroutines = 10
	e := NewEmbedded(DefaultConfig(), nil)

	// Manually set initialized to true so all calls hit ErrAlreadyInitialized
	// (since we can't actually start nv-hostengine in tests)
	e.initialized.Store(true)

	var wg sync.WaitGroup
	var alreadyInitCount atomic.Int32

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			err := e.Init(context.Background())
			if errors.Is(err, ErrAlreadyInitialized) {
				alreadyInitCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// All goroutines should have received ErrAlreadyInitialized
	if got := alreadyInitCount.Load(); got != numGoroutines {
		t.Errorf("ErrAlreadyInitialized count = %d, want %d", got, numGoroutines)
	}
}

func TestEmbedded_Init_ConcurrentRace(t *testing.T) {
	// This test is designed to be run with -race to detect data races.
	// It creates multiple Embedded instances and calls Init concurrently.

	const numInstances = 5
	const numGoroutinesPerInstance = 3

	var wg sync.WaitGroup

	for i := 0; i < numInstances; i++ {
		e := NewEmbedded(DefaultConfig(), nil)

		for j := 0; j < numGoroutinesPerInstance; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Ignore errors - we're just checking for races
				_ = e.Init(context.Background())
			}()
		}
	}

	wg.Wait()
}

func TestEmbedded_Shutdown_NotInitialized(t *testing.T) {
	e := NewEmbedded(DefaultConfig(), nil)

	// Shutdown on uninitialized should succeed (no-op)
	err := e.Shutdown(context.Background())
	if err != nil {
		t.Errorf("Shutdown() error = %v, want nil", err)
	}
}

func TestEmbedded_IsAvailable(t *testing.T) {
	e := NewEmbedded(DefaultConfig(), nil)

	// Initially not available
	if e.IsAvailable() {
		t.Error("IsAvailable() = true before Init, want false")
	}

	// Manually set initialized
	e.initialized.Store(true)
	if !e.IsAvailable() {
		t.Error("IsAvailable() = false after setting initialized, want true")
	}

	// Clear initialized
	e.initialized.Store(false)
	if e.IsAvailable() {
		t.Error("IsAvailable() = true after clearing initialized, want false")
	}
}

func TestEmbedded_Methods_NotInitialized(t *testing.T) {
	e := NewEmbedded(DefaultConfig(), nil)

	// All methods should return ErrNotInitialized when not initialized

	t.Run("WatchFields", func(t *testing.T) {
		err := e.WatchFields(0, DefaultWatchFields(), 0)
		if !errors.Is(err, ErrNotInitialized) {
			t.Errorf("WatchFields() error = %v, want ErrNotInitialized", err)
		}
	})

	t.Run("GetLatestValues", func(t *testing.T) {
		_, err := e.GetLatestValues(0, DefaultWatchFields())
		if !errors.Is(err, ErrNotInitialized) {
			t.Errorf("GetLatestValues() error = %v, want ErrNotInitialized", err)
		}
	})

	t.Run("GetProfilingMetrics", func(t *testing.T) {
		_, err := e.GetProfilingMetrics(0)
		if !errors.Is(err, ErrNotInitialized) {
			t.Errorf("GetProfilingMetrics() error = %v, want ErrNotInitialized", err)
		}
	})

	t.Run("GetNVSwitchStatus", func(t *testing.T) {
		_, err := e.GetNVSwitchStatus()
		if !errors.Is(err, ErrNotInitialized) {
			t.Errorf("GetNVSwitchStatus() error = %v, want ErrNotInitialized", err)
		}
	})

	t.Run("SetHealthPolicy", func(t *testing.T) {
		err := e.SetHealthPolicy(HealthPolicy{})
		if !errors.Is(err, ErrNotInitialized) {
			t.Errorf("SetHealthPolicy() error = %v, want ErrNotInitialized", err)
		}
	})

	t.Run("GetHealthViolations", func(t *testing.T) {
		_, err := e.GetHealthViolations()
		if !errors.Is(err, ErrNotInitialized) {
			t.Errorf("GetHealthViolations() error = %v, want ErrNotInitialized", err)
		}
	})

	t.Run("GetXIDErrors", func(t *testing.T) {
		_, err := e.GetXIDErrors(0, time.Now())
		if !errors.Is(err, ErrNotInitialized) {
			t.Errorf("GetXIDErrors() error = %v, want ErrNotInitialized", err)
		}
	})
}

// TestErrorWrapping_Chain verifies that wrapped errors preserve the full
// error chain, allowing both the sentinel error and underlying cause to
// be extracted using errors.Is() and errors.Unwrap().
//
// This test validates the %w: %w wrapping pattern used in embedded.go.
func TestErrorWrapping_Chain(t *testing.T) {
	// Simulate the error wrapping pattern from embedded.go
	innerErr := errors.New("connection refused")
	wrappedErr := fmt.Errorf("%w: %w", ErrHostengineStartFailed, innerErr)

	t.Run("errors.Is matches sentinel", func(t *testing.T) {
		if !errors.Is(wrappedErr, ErrHostengineStartFailed) {
			t.Errorf("errors.Is(wrappedErr, ErrHostengineStartFailed) = false, want true")
		}
	})

	t.Run("errors.Is matches inner error", func(t *testing.T) {
		if !errors.Is(wrappedErr, innerErr) {
			t.Errorf("errors.Is(wrappedErr, innerErr) = false, want true")
		}
	})

	t.Run("error message contains both", func(t *testing.T) {
		msg := wrappedErr.Error()
		if !contains(msg, "nv-hostengine failed to start") {
			t.Errorf("error message missing sentinel: %s", msg)
		}
		if !contains(msg, "connection refused") {
			t.Errorf("error message missing inner error: %s", msg)
		}
	})
}

// TestErrorWrapping_AllSentinels verifies all sentinel errors used in
// wrapped error patterns can be properly detected.
func TestErrorWrapping_AllSentinels(t *testing.T) {
	tests := []struct {
		name     string
		sentinel error
	}{
		{"ErrHostengineStartFailed", ErrHostengineStartFailed},
		{"ErrConnectionFailed", ErrConnectionFailed},
		{"ErrNotInitialized", ErrNotInitialized},
		{"ErrAlreadyInitialized", ErrAlreadyInitialized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := errors.New("underlying cause")
			wrapped := fmt.Errorf("%w: %w", tt.sentinel, inner)

			// Verify sentinel is detectable
			if !errors.Is(wrapped, tt.sentinel) {
				t.Errorf("errors.Is(wrapped, %s) = false, want true", tt.name)
			}

			// Verify inner error is detectable
			if !errors.Is(wrapped, inner) {
				t.Errorf("errors.Is(wrapped, inner) = false, want true")
			}
		})
	}
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
