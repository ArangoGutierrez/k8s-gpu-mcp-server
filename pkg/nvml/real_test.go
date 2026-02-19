// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration && cgo
// +build integration,cgo

package nvml

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealNVML_Integration tests real NVML with actual GPU hardware.
// Run with: go test -tags=integration ./pkg/nvml/
// Requires: NVIDIA GPU with driver installed
func TestRealNVML_Integration(t *testing.T) {
	real := NewReal()
	ctx := context.Background()

	// Test initialization
	err := real.Init(ctx)
	require.NoError(t, err, "NVML initialization should succeed with GPU present")
	defer func() {
		err := real.Shutdown(ctx)
		assert.NoError(t, err)
	}()

	// Test device count
	count, err := real.GetDeviceCount(ctx)
	require.NoError(t, err)
	assert.Greater(t, count, 0, "Should have at least one GPU")

	t.Logf("Found %d GPU device(s)", count)

	// Test first device
	device, err := real.GetDeviceByIndex(ctx, 0)
	require.NoError(t, err)
	require.NotNil(t, device)

	// Test device properties
	name, err := device.GetName(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, name)
	t.Logf("GPU 0 Name: %s", name)

	uuid, err := device.GetUUID(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, uuid)
	assert.Contains(t, uuid, "GPU-")
	t.Logf("GPU 0 UUID: %s", uuid)

	pciInfo, err := device.GetPCIInfo(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, pciInfo.BusID)
	t.Logf("GPU 0 BusID: %s", pciInfo.BusID)

	memInfo, err := device.GetMemoryInfo(ctx)
	require.NoError(t, err)
	assert.Greater(t, memInfo.Total, uint64(0))
	t.Logf("GPU 0 Memory: %d MB total, %d MB used",
		memInfo.Total/1024/1024, memInfo.Used/1024/1024)

	temp, err := device.GetTemperature(ctx)
	require.NoError(t, err)
	assert.Greater(t, temp, uint32(0))
	assert.Less(t, temp, uint32(150), "Temperature should be reasonable")
	t.Logf("GPU 0 Temperature: %d°C", temp)

	power, err := device.GetPowerUsage(ctx)
	require.NoError(t, err)
	assert.Greater(t, power, uint32(0))
	t.Logf("GPU 0 Power: %.1fW", float64(power)/1000.0)

	util, err := device.GetUtilizationRates(ctx)
	require.NoError(t, err)
	assert.LessOrEqual(t, util.GPU, uint32(100))
	assert.LessOrEqual(t, util.Memory, uint32(100))
	t.Logf("GPU 0 Utilization: GPU=%d%%, Memory=%d%%", util.GPU, util.Memory)
}

func TestRealNVML_ContextCancellation(t *testing.T) {
	real := NewReal()
	ctx := context.Background()

	err := real.Init(ctx)
	require.NoError(t, err)
	defer real.Shutdown(ctx)

	// Create cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Operations should fail with context error
	_, err = real.GetDeviceCount(cancelledCtx)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrContextCancelled), "expected ErrContextCancelled, got %v", err)
}

func TestRealNVML_Timeout(t *testing.T) {
	real := NewReal()
	ctx := context.Background()

	err := real.Init(ctx)
	require.NoError(t, err)
	defer real.Shutdown(ctx)

	// Create context with very short timeout
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond) // Ensure timeout fires

	// Operations should fail with timeout (context deadline exceeded)
	_, err = real.GetDeviceCount(timeoutCtx)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrContextCancelled), "expected ErrContextCancelled, got %v", err)
}

func TestRealNVML_InvalidIndex(t *testing.T) {
	real := NewReal()
	ctx := context.Background()

	err := real.Init(ctx)
	require.NoError(t, err)
	defer real.Shutdown(ctx)

	// Try to get device with invalid index
	_, err = real.GetDeviceByIndex(ctx, 999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get device")
}

func TestRealNVML_UninitializedAccess(t *testing.T) {
	real := NewReal()
	ctx := context.Background()

	// Try to use without initialization
	_, err := real.GetDeviceCount(ctx)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotInitialized), "expected ErrNotInitialized, got %v", err)
}

// TestRealNVML_ConcurrentInit verifies that concurrent Init calls are safe.
// Run with: go test -race -tags=integration ./pkg/nvml/
func TestRealNVML_ConcurrentInit(t *testing.T) {
	real := NewReal()
	ctx := context.Background()
	defer real.Shutdown(ctx)

	const goroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, goroutines)

	// Launch multiple concurrent Init calls
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := real.Init(ctx); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// All Init calls should succeed (or be no-ops)
	for err := range errors {
		t.Errorf("concurrent Init failed: %v", err)
	}

	// Verify NVML is functional after concurrent init
	count, err := real.GetDeviceCount(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 0)

	// Verify capabilities were probed exactly once (via sync.Once)
	caps, err := real.GetCapabilities(ctx)
	require.NoError(t, err)
	assert.NotNil(t, caps, "Capabilities should be set after concurrent Init")
	assert.NotEmpty(t, caps.Tier.String(), "Capabilities tier should be determined")
}

// TestRealNVML_ConcurrentShutdown verifies that concurrent Shutdown calls
// are safe.
// Run with: go test -race -tags=integration ./pkg/nvml/
func TestRealNVML_ConcurrentShutdown(t *testing.T) {
	real := NewReal()
	ctx := context.Background()

	// Initialize first
	err := real.Init(ctx)
	require.NoError(t, err)

	const goroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, goroutines)

	// Launch multiple concurrent Shutdown calls
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := real.Shutdown(ctx); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// All Shutdown calls should succeed (or be no-ops)
	for err := range errors {
		t.Errorf("concurrent Shutdown failed: %v", err)
	}
}

// TestRealNVML_ConcurrentInitShutdown verifies that interleaved Init and
// Shutdown calls are safe.
// Run with: go test -race -tags=integration ./pkg/nvml/
func TestRealNVML_ConcurrentInitShutdown(t *testing.T) {
	real := NewReal()
	ctx := context.Background()
	defer real.Shutdown(ctx)

	const goroutines = 20
	var wg sync.WaitGroup

	// Launch interleaved Init and Shutdown calls
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				_ = real.Init(ctx)
			} else {
				_ = real.Shutdown(ctx)
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race detected (run with -race flag)
}
