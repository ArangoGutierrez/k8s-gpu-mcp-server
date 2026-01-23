// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestStubFallback_Pattern verifies the recommended fallback pattern works correctly.
// This simulates what cmd/agent should do when DCGM is unavailable.
func TestStubFallback_Pattern(t *testing.T) {
	ctx := context.Background()

	// Simulate the agent initialization pattern from doc.go
	var dcgmClient Interface

	// Try embedded mode first (will fail in test environment without nv-hostengine)
	embedded := NewEmbedded(DefaultConfig(), nil)
	if err := embedded.Init(ctx); err != nil {
		// Expected: fall back to stub
		t.Logf("DCGM unavailable (expected in test): %v", err)
		dcgmClient = NewStub()
	} else {
		dcgmClient = embedded
		defer func() { _ = embedded.Shutdown(ctx) }()
	}

	// Verify we have a valid client (either embedded or stub)
	if dcgmClient == nil {
		t.Fatal("dcgmClient should not be nil after fallback")
	}

	// The stub should always return false for IsAvailable
	// This allows callers to check before using DCGM-specific features
	if dcgmClient.IsAvailable() {
		// If we got here, we actually have DCGM available (unlikely in test)
		t.Log("DCGM is available (unexpected in test environment)")
	} else {
		t.Log("DCGM not available, using NVML-only mode (expected)")
	}
}

// TestStubFallback_AllMethodsReturnUnavailable verifies that the Stub
// implementation consistently returns ErrDCGMUnavailable for all data methods.
// This is critical for graceful degradation.
func TestStubFallback_AllMethodsReturnUnavailable(t *testing.T) {
	stub := NewStub()
	ctx := context.Background()

	// Init and Shutdown should succeed (no-op)
	if err := stub.Init(ctx); err != nil {
		t.Errorf("Stub.Init should succeed, got %v", err)
	}
	if err := stub.Shutdown(ctx); err != nil {
		t.Errorf("Stub.Shutdown should succeed, got %v", err)
	}

	// IsAvailable should return false
	if stub.IsAvailable() {
		t.Error("Stub.IsAvailable should return false")
	}

	// All data methods should return ErrDCGMUnavailable
	tests := []struct {
		name string
		fn   func() error
	}{
		{
			name: "WatchFields",
			fn: func() error {
				return stub.WatchFields(0, DefaultWatchFields(), time.Second)
			},
		},
		{
			name: "GetLatestValues",
			fn: func() error {
				_, err := stub.GetLatestValues(0, DefaultWatchFields())
				return err
			},
		},
		{
			name: "GetProfilingMetrics",
			fn: func() error {
				_, err := stub.GetProfilingMetrics(0)
				return err
			},
		},
		{
			name: "GetNVSwitchStatus",
			fn: func() error {
				_, err := stub.GetNVSwitchStatus()
				return err
			},
		},
		{
			name: "SetHealthPolicy",
			fn: func() error {
				return stub.SetHealthPolicy(HealthPolicy{Name: "test"})
			},
		},
		{
			name: "GetHealthViolations",
			fn: func() error {
				_, err := stub.GetHealthViolations()
				return err
			},
		},
		{
			name: "GetXIDErrors",
			fn: func() error {
				_, err := stub.GetXIDErrors(0, time.Now())
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if !errors.Is(err, ErrDCGMUnavailable) {
				t.Errorf("%s should return ErrDCGMUnavailable, got %v", tt.name, err)
			}
		})
	}
}

// TestStubFallback_SafeToCallMultipleTimes verifies that Stub methods
// can be called multiple times safely (idempotent).
func TestStubFallback_SafeToCallMultipleTimes(t *testing.T) {
	stub := NewStub()
	ctx := context.Background()

	// Multiple Init calls should succeed
	for i := 0; i < 3; i++ {
		if err := stub.Init(ctx); err != nil {
			t.Errorf("Init call %d failed: %v", i, err)
		}
	}

	// Multiple Shutdown calls should succeed
	for i := 0; i < 3; i++ {
		if err := stub.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown call %d failed: %v", i, err)
		}
	}

	// IsAvailable should consistently return false
	for i := 0; i < 3; i++ {
		if stub.IsAvailable() {
			t.Errorf("IsAvailable call %d returned true", i)
		}
	}
}

// TestMockFallback_SimulateUnavailable verifies that Mock can simulate
// DCGM being unavailable for testing purposes.
func TestMockFallback_SimulateUnavailable(t *testing.T) {
	mock := NewMock(2)

	// Simulate DCGM becoming unavailable
	mock.SetAvailable(false)

	ctx := context.Background()
	err := mock.Init(ctx)

	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("Mock.Init with available=false should return ErrDCGMUnavailable, got %v", err)
	}
}

// TestGracefulDegradation_FeatureDetection demonstrates how callers should
// check for DCGM features before using them.
func TestGracefulDegradation_FeatureDetection(t *testing.T) {
	// Simulate two scenarios: with and without DCGM

	scenarios := []struct {
		name            string
		client          Interface
		expectAvailable bool
	}{
		{
			name:            "with_mock_dcgm",
			client:          func() Interface { m := NewMock(2); _ = m.Init(context.Background()); return m }(),
			expectAvailable: true,
		},
		{
			name:            "without_dcgm_stub",
			client:          NewStub(),
			expectAvailable: false,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			// Check availability first (recommended pattern)
			available := sc.client.IsAvailable()

			if available != sc.expectAvailable {
				t.Errorf("IsAvailable() = %v, want %v", available, sc.expectAvailable)
			}

			// Only use DCGM features if available
			if available {
				metrics, err := sc.client.GetProfilingMetrics(0)
				if err != nil {
					t.Errorf("GetProfilingMetrics should succeed when available: %v", err)
					return
				}
				if metrics == nil {
					t.Error("metrics should not be nil when available")
					return
				}
				t.Logf("SM Occupancy: %.1f%%", metrics.SMOccupancy)
			} else {
				// Verify methods return appropriate errors
				_, err := sc.client.GetProfilingMetrics(0)
				if err == nil {
					t.Error("GetProfilingMetrics should fail when unavailable")
				}
				t.Logf("DCGM unavailable (expected): %v", err)
			}
		})
	}
}

// TestInterfacePolymorphism verifies that all implementations can be used
// interchangeably through the Interface type.
func TestInterfacePolymorphism(t *testing.T) {
	implementations := []struct {
		name   string
		create func() Interface
	}{
		{"Stub", func() Interface { return NewStub() }},
		{"Mock", func() Interface { return NewMock(2) }},
		{"Embedded", func() Interface { return NewEmbedded(DefaultConfig(), nil) }},
	}

	for _, impl := range implementations {
		t.Run(impl.name, func(t *testing.T) {
			client := impl.create()

			// All implementations should satisfy Interface
			if client == nil {
				t.Fatal("client should not be nil")
			}

			// Init should not panic (may return error)
			_ = client.Init(context.Background())

			// IsAvailable should not panic
			_ = client.IsAvailable()

			// Shutdown should not panic
			_ = client.Shutdown(context.Background())
		})
	}
}

// TestConfigValidation_Integration verifies config validation works with
// the actual implementations.
func TestConfigValidation_Integration(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "valid_embedded_config",
			config:  DefaultConfig(),
			wantErr: false,
		},
		{
			name: "valid_external_config",
			config: Config{
				Enabled:       true,
				Mode:          "external",
				Socket:        "/var/run/dcgm.sock",
				WatchInterval: time.Second,
			},
			wantErr: false,
		},
		{
			name: "invalid_mode",
			config: Config{
				Enabled: true,
				Mode:    "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
