// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package blackbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
)

// Recorder errors.
var (
	ErrAlreadyStarted = errors.New("recorder already started")
	ErrNotStarted     = errors.New("recorder not started")
	ErrGPUNotFound    = errors.New("GPU not found")
	ErrNoSnapshots    = errors.New("no snapshots available")
)

// Recorder continuously captures GPU telemetry into per-GPU ring buffers.
// It provides query methods to retrieve historical data for failure analysis.
type Recorder struct {
	// Configuration
	config     RecorderConfig
	nvmlClient nvml.Interface
	logger     *slog.Logger

	// State
	gpuBuffers map[string]*RingBuffer[GPUSnapshot] // UUID -> buffer
	mu         sync.RWMutex                        // Protects gpuBuffers

	// Lifecycle
	running atomic.Bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewRecorder creates a new Flight Recorder.
// The recorder must be started with Start() before it begins capturing data.
func NewRecorder(
	nvmlClient nvml.Interface,
	config RecorderConfig,
	opts ...RecorderOption,
) *Recorder {
	r := &Recorder{
		config:     config,
		nvmlClient: nvmlClient,
		gpuBuffers: make(map[string]*RingBuffer[GPUSnapshot]),
		logger:     slog.Default(),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// RecorderOption configures a Recorder.
type RecorderOption func(*Recorder)

// WithLogger sets the logger for the recorder.
func WithLogger(logger *slog.Logger) RecorderOption {
	return func(r *Recorder) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// Start begins the background sampling goroutine.
// Returns ErrAlreadyStarted if the recorder is already running.
// The provided context is used for the initial GPU enumeration only;
// use Stop() to shut down the recorder.
func (r *Recorder) Start(ctx context.Context) error {
	if err := r.config.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	if !r.running.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}

	// Initialize NVML and enumerate GPUs
	if err := r.nvmlClient.Init(ctx); err != nil {
		r.running.Store(false)
		return fmt.Errorf("nvml init: %w", err)
	}

	// Discover GPUs and create buffers
	if err := r.discoverGPUs(ctx); err != nil {
		_ = r.nvmlClient.Shutdown(ctx)
		r.running.Store(false)
		return fmt.Errorf("discover GPUs: %w", err)
	}

	r.stopCh = make(chan struct{})
	r.wg.Add(1)
	go r.sampleLoop()

	r.logger.Info("flight recorder started",
		"interval", r.config.Interval,
		"retention", r.config.Retention,
		"gpus", len(r.gpuBuffers),
	)

	return nil
}

// Stop gracefully shuts down the recorder.
// It waits for any in-progress sampling to complete.
// Safe to call multiple times or if not started.
func (r *Recorder) Stop() {
	if !r.running.CompareAndSwap(true, false) {
		return // Not running or already stopping
	}

	close(r.stopCh)
	r.wg.Wait()

	// Shutdown NVML
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.nvmlClient.Shutdown(ctx)

	r.logger.Info("flight recorder stopped")
}

// IsRunning returns true if the recorder is actively sampling.
func (r *Recorder) IsRunning() bool {
	return r.running.Load()
}

// GPUCount returns the number of GPUs being tracked.
func (r *Recorder) GPUCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.gpuBuffers)
}

// GetSnapshot returns the snapshot nearest to the given time for the GPU.
// Returns ErrGPUNotFound if the UUID is unknown, or ErrNoSnapshots if
// no data has been captured yet.
func (r *Recorder) GetSnapshot(gpuUUID string, at time.Time) (*GPUSnapshot, error) {
	r.mu.RLock()
	buf, ok := r.gpuBuffers[gpuUUID]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrGPUNotFound, gpuUUID)
	}

	snap, found := buf.FindNearest(at, func(s GPUSnapshot) time.Time {
		return s.Timestamp
	})
	if !found {
		return nil, ErrNoSnapshots
	}

	return &snap, nil
}

// GetTimeline returns snapshots for the past duration for the given GPU.
// Results are in chronological order (oldest first).
// Returns ErrGPUNotFound if the UUID is unknown.
func (r *Recorder) GetTimeline(
	gpuUUID string,
	duration time.Duration,
) ([]GPUSnapshot, error) {
	r.mu.RLock()
	buf, ok := r.gpuBuffers[gpuUUID]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrGPUNotFound, gpuUUID)
	}

	since := time.Now().Add(-duration)
	return buf.Query(since, func(s GPUSnapshot) time.Time {
		return s.Timestamp
	}), nil
}

// GetLatestAll returns the most recent snapshot for each tracked GPU.
// Returns an empty map if no GPUs are tracked or no data captured yet.
func (r *Recorder) GetLatestAll() map[string]*GPUSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*GPUSnapshot, len(r.gpuBuffers))
	for uuid, buf := range r.gpuBuffers {
		if snap, ok := buf.Latest(); ok {
			result[uuid] = &snap
		}
	}
	return result
}

// GetAllTimelines returns all snapshots for all GPUs for the past duration.
// Useful for correlation analysis across multiple GPUs.
func (r *Recorder) GetAllTimelines(
	duration time.Duration,
) map[string][]GPUSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	since := time.Now().Add(-duration)
	result := make(map[string][]GPUSnapshot, len(r.gpuBuffers))

	for uuid, buf := range r.gpuBuffers {
		timeline := buf.Query(since, func(s GPUSnapshot) time.Time {
			return s.Timestamp
		})
		if len(timeline) > 0 {
			result[uuid] = timeline
		}
	}

	return result
}

// TrackedGPUs returns the UUIDs of all tracked GPUs.
func (r *Recorder) TrackedGPUs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	uuids := make([]string, 0, len(r.gpuBuffers))
	for uuid := range r.gpuBuffers {
		uuids = append(uuids, uuid)
	}
	return uuids
}

// discoverGPUs enumerates GPUs and creates ring buffers for each.
func (r *Recorder) discoverGPUs(ctx context.Context) error {
	count, err := r.nvmlClient.GetDeviceCount(ctx)
	if err != nil {
		return fmt.Errorf("get device count: %w", err)
	}

	capacity := r.config.BufferCapacity()
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := 0; i < count; i++ {
		dev, err := r.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			r.logger.Warn("failed to get device",
				"index", i,
				"error", err,
			)
			continue
		}

		uuid, err := dev.GetUUID(ctx)
		if err != nil {
			r.logger.Warn("failed to get device UUID",
				"index", i,
				"error", err,
			)
			continue
		}

		r.gpuBuffers[uuid] = NewRingBuffer[GPUSnapshot](capacity)
		r.logger.Debug("tracking GPU",
			"index", i,
			"uuid", uuid,
			"buffer_capacity", capacity,
		)
	}

	return nil
}

// sampleLoop is the background goroutine that periodically samples all GPUs.
func (r *Recorder) sampleLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()

	// Take an initial sample immediately
	r.sampleAllGPUs()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.sampleAllGPUs()
		}
	}
}

// sampleAllGPUs captures a snapshot from each tracked GPU.
func (r *Recorder) sampleAllGPUs() {
	ctx, cancel := context.WithTimeout(context.Background(), r.config.Interval/2)
	defer cancel()

	now := time.Now()

	count, err := r.nvmlClient.GetDeviceCount(ctx)
	if err != nil {
		r.logger.Error("failed to get device count", "error", err)
		return
	}

	for i := 0; i < count; i++ {
		dev, err := r.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			r.logger.Debug("failed to get device", "index", i, "error", err)
			continue
		}

		snap, err := r.captureSnapshot(ctx, dev, i, now)
		if err != nil {
			r.logger.Debug("failed to capture snapshot",
				"index", i,
				"error", err,
			)
			continue
		}

		r.mu.RLock()
		buf, ok := r.gpuBuffers[snap.UUID]
		r.mu.RUnlock()

		if !ok {
			// New GPU detected (hotplug), add buffer
			r.mu.Lock()
			if _, exists := r.gpuBuffers[snap.UUID]; !exists {
				r.gpuBuffers[snap.UUID] = NewRingBuffer[GPUSnapshot](
					r.config.BufferCapacity(),
				)
				r.logger.Info("detected new GPU",
					"uuid", snap.UUID,
					"index", i,
				)
			}
			buf = r.gpuBuffers[snap.UUID]
			r.mu.Unlock()
		}

		buf.Add(snap)
	}
}

// captureSnapshot collects all telemetry for a single GPU.
func (r *Recorder) captureSnapshot(
	ctx context.Context,
	dev nvml.Device,
	index int,
	timestamp time.Time,
) (GPUSnapshot, error) {
	snap := GPUSnapshot{
		Timestamp: timestamp,
		Index:     index,
	}

	var err error

	// UUID (required)
	snap.UUID, err = dev.GetUUID(ctx)
	if err != nil {
		return snap, fmt.Errorf("get UUID: %w", err)
	}

	// Temperature
	snap.Temperature, _ = dev.GetTemperature(ctx)
	snap.TempThreshold, _ = dev.GetTemperatureThreshold(
		ctx,
		nvml.TempThresholdSlowdown,
	)

	// Power
	snap.PowerMW, _ = dev.GetPowerUsage(ctx)
	snap.PowerLimitMW, _ = dev.GetPowerManagementLimit(ctx)

	// Memory
	if memInfo, err := dev.GetMemoryInfo(ctx); err == nil {
		snap.MemUsed = memInfo.Used
		snap.MemTotal = memInfo.Total
	}

	// Utilization
	if util, err := dev.GetUtilizationRates(ctx); err == nil {
		snap.GPUUtil = util.GPU
		snap.MemUtil = util.Memory
	}

	// Clocks
	snap.SMClock, _ = dev.GetClockInfo(ctx, nvml.ClockGraphics)
	snap.MemClock, _ = dev.GetClockInfo(ctx, nvml.ClockMemory)

	// Throttling
	snap.Throttling, _ = dev.GetCurrentClocksThrottleReasons(ctx)

	// ECC errors
	snap.ECCCorrectable, _ = dev.GetTotalEccErrors(ctx, nvml.EccErrorCorrectable)
	snap.ECCUncorrectable, _ = dev.GetTotalEccErrors(ctx, nvml.EccErrorUncorrectable)

	return snap, nil
}
