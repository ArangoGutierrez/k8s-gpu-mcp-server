// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package blackbox

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
)

// testConfig returns a config suitable for testing.
// Uses minimum valid intervals for faster test execution.
func testConfig() RecorderConfig {
	return RecorderConfig{
		Interval:        1 * time.Second,
		Retention:       1 * time.Minute,
		EnableProcesses: false,
	}
}

func TestRecorderConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  RecorderConfig
		wantErr error
	}{
		{
			name:    "default config is valid",
			config:  DefaultConfig(),
			wantErr: nil,
		},
		{
			name:    "test config is valid",
			config:  testConfig(),
			wantErr: nil,
		},
		{
			name: "interval too small",
			config: RecorderConfig{
				Interval:  500 * time.Millisecond, // Less than 1s minimum
				Retention: 30 * time.Minute,
			},
			wantErr: ErrInvalidInterval,
		},
		{
			name: "retention too small",
			config: RecorderConfig{
				Interval:  10 * time.Second,
				Retention: 30 * time.Second,
			},
			wantErr: ErrInvalidRetention,
		},
		{
			name: "retention too large",
			config: RecorderConfig{
				Interval:  10 * time.Second,
				Retention: 3 * time.Hour,
			},
			wantErr: ErrInvalidRetention,
		},
		{
			name: "retention less than interval",
			config: RecorderConfig{
				Interval:  2 * time.Minute,
				Retention: 1 * time.Minute,
			},
			wantErr: ErrRetentionTooLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRecorderConfig_BufferCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   RecorderConfig
		expected int
	}{
		{
			name:     "default config",
			config:   DefaultConfig(),
			expected: 180, // 30min / 10s
		},
		{
			name: "custom config",
			config: RecorderConfig{
				Interval:  5 * time.Second,
				Retention: 1 * time.Hour,
			},
			expected: 720, // 60min / 5s
		},
		{
			name: "zero interval",
			config: RecorderConfig{
				Interval:  0,
				Retention: 30 * time.Minute,
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.config.BufferCapacity()
			if got != tt.expected {
				t.Errorf("BufferCapacity() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestNewRecorder(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(2)
	config := testConfig()

	recorder := NewRecorder(mock, config)

	if recorder == nil {
		t.Fatal("NewRecorder returned nil")
	}
	if recorder.IsRunning() {
		t.Error("new recorder should not be running")
	}
	if recorder.GPUCount() != 0 {
		t.Errorf("new recorder should have 0 GPUs tracked, got %d", recorder.GPUCount())
	}
}

func TestRecorder_StartStop(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(2)
	config := testConfig()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Suppress info logs in tests
	}))

	recorder := NewRecorder(mock, config, WithLogger(logger))

	ctx := context.Background()

	// Start
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if !recorder.IsRunning() {
		t.Error("recorder should be running after Start()")
	}

	if recorder.GPUCount() != 2 {
		t.Errorf("got %d GPUs, want 2", recorder.GPUCount())
	}

	// Stop
	recorder.Stop()

	if recorder.IsRunning() {
		t.Error("recorder should not be running after Stop()")
	}
}

func TestRecorder_StartTwice(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()

	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	// Second start should fail
	err := recorder.Start(ctx)
	if err != ErrAlreadyStarted {
		t.Errorf("second Start() = %v, want ErrAlreadyStarted", err)
	}
}

func TestRecorder_StopIdempotent(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	// Stop without start (should not panic)
	recorder.Stop()

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Multiple stops (should not panic)
	recorder.Stop()
	recorder.Stop()
}

func TestRecorder_InvalidConfig(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := RecorderConfig{
		Interval:  500 * time.Millisecond, // Too short
		Retention: 30 * time.Minute,
	}
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	err := recorder.Start(ctx)

	if err == nil {
		recorder.Stop()
		t.Fatal("Start() should fail with invalid config")
	}
}

func TestRecorder_SampleSingleGPU(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	// Wait for a few samples
	time.Sleep(2500 * time.Millisecond)

	// Check we have data
	gpus := recorder.TrackedGPUs()
	if len(gpus) != 1 {
		t.Fatalf("got %d tracked GPUs, want 1", len(gpus))
	}

	latest := recorder.GetLatestAll()
	if len(latest) != 1 {
		t.Fatalf("GetLatestAll() returned %d GPUs, want 1", len(latest))
	}

	snap := latest[gpus[0]]
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if snap.UUID != gpus[0] {
		t.Errorf("snapshot UUID = %q, want %q", snap.UUID, gpus[0])
	}
	if snap.Index != 0 {
		t.Errorf("snapshot Index = %d, want 0", snap.Index)
	}
}

func TestRecorder_SampleMultipleGPUs(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(4)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	// Wait for samples
	time.Sleep(2500 * time.Millisecond)

	if recorder.GPUCount() != 4 {
		t.Errorf("got %d GPUs, want 4", recorder.GPUCount())
	}

	latest := recorder.GetLatestAll()
	if len(latest) != 4 {
		t.Errorf("GetLatestAll() returned %d GPUs, want 4", len(latest))
	}

	// Verify each GPU has distinct data
	seenIndices := make(map[int]bool)
	for uuid, snap := range latest {
		if snap == nil {
			t.Errorf("snapshot for %s is nil", uuid)
			continue
		}
		if seenIndices[snap.Index] {
			t.Errorf("duplicate index %d", snap.Index)
		}
		seenIndices[snap.Index] = true
	}
}

func TestRecorder_GetTimeline(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	// Wait for multiple samples
	time.Sleep(2500 * time.Millisecond)

	gpus := recorder.TrackedGPUs()
	if len(gpus) == 0 {
		t.Fatal("no GPUs tracked")
	}

	timeline, err := recorder.GetTimeline(gpus[0], 1*time.Minute)
	if err != nil {
		t.Fatalf("GetTimeline() error: %v", err)
	}

	if len(timeline) < 2 {
		t.Errorf("got %d snapshots, want >= 2", len(timeline))
	}

	// Verify chronological order
	for i := 1; i < len(timeline); i++ {
		if timeline[i].Timestamp.Before(timeline[i-1].Timestamp) {
			t.Error("timeline not in chronological order")
		}
	}
}

func TestRecorder_GetSnapshot(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	// Wait for samples
	time.Sleep(2500 * time.Millisecond)

	gpus := recorder.TrackedGPUs()
	if len(gpus) == 0 {
		t.Fatal("no GPUs tracked")
	}

	// Get snapshot at current time
	snap, err := recorder.GetSnapshot(gpus[0], time.Now())
	if err != nil {
		t.Fatalf("GetSnapshot() error: %v", err)
	}

	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if snap.UUID != gpus[0] {
		t.Errorf("snapshot UUID = %q, want %q", snap.UUID, gpus[0])
	}
}

func TestRecorder_GetSnapshotUnknownGPU(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	_, err := recorder.GetSnapshot("unknown-uuid", time.Now())
	if err == nil {
		t.Error("GetSnapshot() should return error for unknown GPU")
	}
}

func TestRecorder_GetTimelineUnknownGPU(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	_, err := recorder.GetTimeline("unknown-uuid", 1*time.Minute)
	if err == nil {
		t.Error("GetTimeline() should return error for unknown GPU")
	}
}

func TestRecorder_GetLatestAll_Empty(t *testing.T) {
	t.Parallel()

	// Use zero-GPU mock to test empty case
	mock := &zeroGPUMock{}
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	latest := recorder.GetLatestAll()
	if len(latest) != 0 {
		t.Errorf("GetLatestAll() returned %d GPUs, want 0", len(latest))
	}
}

func TestRecorder_GetAllTimelines(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(2)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	// Wait for samples
	time.Sleep(2500 * time.Millisecond)

	timelines := recorder.GetAllTimelines(1 * time.Minute)
	if len(timelines) != 2 {
		t.Errorf("GetAllTimelines() returned %d GPUs, want 2", len(timelines))
	}

	for uuid, timeline := range timelines {
		if len(timeline) < 2 {
			t.Errorf("GPU %s has %d snapshots, want >= 2", uuid, len(timeline))
		}
	}
}

func TestRecorder_ContextCancellation(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx, cancel := context.WithCancel(context.Background())

	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Cancel context (shouldn't affect running recorder)
	cancel()

	// Recorder should still be running
	time.Sleep(1500 * time.Millisecond)
	if !recorder.IsRunning() {
		t.Error("recorder should still be running after context cancel")
	}

	// Clean stop
	recorder.Stop()
	if recorder.IsRunning() {
		t.Error("recorder should stop after Stop()")
	}
}

func TestRecorder_NoGPUs(t *testing.T) {
	t.Parallel()

	// nvml.NewMock(0) defaults to 2 GPUs per its contract.
	// Use a custom zero-GPU mock to test this scenario.
	mock := &zeroGPUMock{}
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	if recorder.GPUCount() != 0 {
		t.Errorf("got %d GPUs, want 0", recorder.GPUCount())
	}

	gpus := recorder.TrackedGPUs()
	if len(gpus) != 0 {
		t.Errorf("got %d tracked GPUs, want 0", len(gpus))
	}
}

// zeroGPUMock is a mock NVML client with zero GPUs for testing edge cases.
type zeroGPUMock struct {
	nvml.UnimplementedInterface
}

func (m *zeroGPUMock) Init(ctx context.Context) error     { return nil }
func (m *zeroGPUMock) Shutdown(ctx context.Context) error { return nil }
func (m *zeroGPUMock) GetDeviceCount(ctx context.Context) (int, error) {
	return 0, nil
}
func (m *zeroGPUMock) GetDeviceByIndex(
	ctx context.Context,
	idx int,
) (nvml.Device, error) {
	return nil, nvml.ErrInvalidDevice
}

func TestRecorder_SnapshotData(t *testing.T) {
	t.Parallel()

	mock := nvml.NewMock(1)
	config := testConfig()
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	time.Sleep(1500 * time.Millisecond)

	gpus := recorder.TrackedGPUs()
	snap, err := recorder.GetSnapshot(gpus[0], time.Now())
	if err != nil {
		t.Fatalf("GetSnapshot() error: %v", err)
	}

	// Verify snapshot has expected mock data
	// Mock device 0 has: temp=45, power=150000, memUsed=8GB
	if snap.Temperature == 0 {
		t.Error("Temperature should not be 0")
	}
	if snap.PowerMW == 0 {
		t.Error("PowerMW should not be 0")
	}
	if snap.MemUsed == 0 {
		t.Error("MemUsed should not be 0")
	}
	if snap.MemTotal == 0 {
		t.Error("MemTotal should not be 0")
	}
}

func TestRecorder_MemoryBudget(t *testing.T) {
	t.Parallel()

	// Test memory usage with 8 GPUs
	mock := nvml.NewMock(8)
	config := RecorderConfig{
		Interval:        1 * time.Second,
		Retention:       1 * time.Minute, // 60 snapshots per GPU
		EnableProcesses: false,
	}
	recorder := NewRecorder(mock, config)

	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer recorder.Stop()

	// Wait for a couple of samples
	time.Sleep(2500 * time.Millisecond)

	// Force GC and get memory stats
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Log memory usage for reference
	t.Logf("Heap alloc: %d KB", m.HeapAlloc/1024)
	t.Logf("Heap in use: %d KB", m.HeapInuse/1024)

	// With full retention settings (180 snapshots × 8 GPUs × ~1KB each),
	// we expect < 2MB for the recorder data.
	// This test uses smaller settings, so memory should be even lower.
	// We can't precisely measure recorder memory vs other allocations,
	// so this is more of a sanity check.

	// Verify all GPUs have data
	latest := recorder.GetLatestAll()
	if len(latest) != 8 {
		t.Errorf("got %d GPUs, want 8", len(latest))
	}
}

func TestGPUSnapshot_HelperMethods(t *testing.T) {
	t.Parallel()

	t.Run("IsThrottled", func(t *testing.T) {
		snap := GPUSnapshot{Throttling: 0}
		if snap.IsThrottled() {
			t.Error("IsThrottled() should be false when Throttling=0")
		}

		snap.Throttling = nvml.ThrottleReasonHwSlowdown
		if !snap.IsThrottled() {
			t.Error("IsThrottled() should be true when Throttling!=0")
		}
	})

	t.Run("HasECCErrors", func(t *testing.T) {
		snap := GPUSnapshot{}
		if snap.HasECCErrors() {
			t.Error("HasECCErrors() should be false with no errors")
		}

		snap.ECCCorrectable = 5
		if !snap.HasECCErrors() {
			t.Error("HasECCErrors() should be true with correctable errors")
		}

		snap.ECCCorrectable = 0
		snap.ECCUncorrectable = 1
		if !snap.HasECCErrors() {
			t.Error("HasECCErrors() should be true with uncorrectable errors")
		}
	})

	t.Run("MemoryUsagePercent", func(t *testing.T) {
		snap := GPUSnapshot{MemUsed: 0, MemTotal: 0}
		if snap.MemoryUsagePercent() != 0 {
			t.Error("MemoryUsagePercent() should be 0 with no total")
		}

		snap.MemTotal = 1000
		snap.MemUsed = 250
		pct := snap.MemoryUsagePercent()
		if pct != 25.0 {
			t.Errorf("MemoryUsagePercent() = %f, want 25.0", pct)
		}
	})

	t.Run("PowerUsagePercent", func(t *testing.T) {
		snap := GPUSnapshot{PowerMW: 0, PowerLimitMW: 0}
		if snap.PowerUsagePercent() != 0 {
			t.Error("PowerUsagePercent() should be 0 with no limit")
		}

		snap.PowerLimitMW = 400000
		snap.PowerMW = 200000
		pct := snap.PowerUsagePercent()
		if pct != 50.0 {
			t.Errorf("PowerUsagePercent() = %f, want 50.0", pct)
		}
	})
}
