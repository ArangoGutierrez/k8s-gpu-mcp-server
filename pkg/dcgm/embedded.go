// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Embedded implements Interface using an embedded nv-hostengine process.
// This mode starts nv-hostengine internally, making the agent self-contained.
type Embedded struct {
	config Config
	logger *slog.Logger

	// Embedded hostengine process
	hostengine     *exec.Cmd
	hostenginePort int

	// Internal client for DCGM API calls
	client *Client

	// State
	mu          sync.RWMutex
	initialized atomic.Bool
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewEmbedded creates a new embedded DCGM implementation.
// The nv-hostengine binary must be present in PATH.
func NewEmbedded(config Config, logger *slog.Logger) *Embedded {
	if logger == nil {
		logger = slog.Default()
	}
	if config.EmbeddedPort <= 0 {
		config.EmbeddedPort = 5555
	}
	return &Embedded{
		config:         config,
		logger:         logger,
		hostenginePort: config.EmbeddedPort,
	}
}

// Init starts nv-hostengine and connects to it.
func (e *Embedded) Init(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check initialized state under lock to prevent race conditions
	// between concurrent Init() calls.
	if e.initialized.Load() {
		return ErrAlreadyInitialized
	}

	// Verify nv-hostengine is available
	hostengine, err := exec.LookPath("nv-hostengine")
	if err != nil {
		e.logger.Warn("nv-hostengine not found, DCGM unavailable",
			"error", err,
		)
		return ErrHostengineNotFound
	}

	e.logger.Info("starting embedded nv-hostengine",
		"binary", hostengine,
		"port", e.hostenginePort,
	)

	// Start nv-hostengine in embedded mode (no daemon)
	// Use context.Background() instead of the passed ctx because nv-hostengine
	// is a long-lived process that should not be killed when Init's context
	// times out or is cancelled. Shutdown is handled via stopCh and cleanup().
	e.stopCh = make(chan struct{})
	e.hostengine = exec.CommandContext(context.Background(), hostengine,
		"--no-daemon",
		"-p", fmt.Sprintf("%d", e.hostenginePort),
	)

	// Capture stderr for debugging
	e.hostengine.Stderr = os.Stderr

	if err := e.hostengine.Start(); err != nil {
		e.logger.Error("failed to start nv-hostengine",
			"error", err,
		)
		return fmt.Errorf("%w: %w", ErrHostengineStartFailed, err)
	}

	// Start process monitor
	e.wg.Add(1)
	go e.monitorHostengine()

	// Wait for nv-hostengine to be ready
	e.logger.Debug("waiting for nv-hostengine startup")
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := e.waitForReady(readyCtx); err != nil {
		e.logger.Error("nv-hostengine failed to become ready",
			"error", err,
		)
		e.cleanup()
		return fmt.Errorf("%w: %w", ErrHostengineStartFailed, err)
	}

	// Create and connect client
	e.client = NewClient(fmt.Sprintf("localhost:%d", e.hostenginePort), e.logger)
	if err := e.client.Connect(ctx); err != nil {
		e.logger.Error("failed to connect to nv-hostengine",
			"error", err,
		)
		e.cleanup()
		return fmt.Errorf("%w: %w", ErrConnectionFailed, err)
	}

	e.initialized.Store(true)
	e.logger.Info("embedded DCGM initialized",
		"port", e.hostenginePort,
	)

	return nil
}

// waitForReady polls until nv-hostengine is ready to accept connections.
func (e *Embedded) waitForReady(ctx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Try to connect
			client := NewClient(fmt.Sprintf("localhost:%d", e.hostenginePort), e.logger)
			if err := client.Connect(ctx); err == nil {
				_ = client.Disconnect()
				return nil
			}
		}
	}
}

// monitorHostengine watches the hostengine process and handles crashes.
func (e *Embedded) monitorHostengine() {
	defer e.wg.Done()

	if e.hostengine == nil || e.hostengine.Process == nil {
		return
	}

	// Wait for process to exit
	err := e.hostengine.Wait()

	select {
	case <-e.stopCh:
		// Expected shutdown
		return
	default:
		// Unexpected exit
		e.logger.Error("nv-hostengine crashed",
			"error", err,
			"exit_code", e.hostengine.ProcessState.ExitCode(),
		)
		e.initialized.Store(false)
	}
}

// cleanup stops the hostengine process.
func (e *Embedded) cleanup() {
	if e.stopCh != nil {
		close(e.stopCh)
	}

	if e.client != nil {
		_ = e.client.Disconnect()
	}

	if e.hostengine != nil && e.hostengine.Process != nil {
		_ = e.hostengine.Process.Kill()
		_ = e.hostengine.Wait()
	}
}

// Shutdown stops the embedded nv-hostengine.
func (e *Embedded) Shutdown(ctx context.Context) error {
	if !e.initialized.CompareAndSwap(true, false) {
		return nil // Not running
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Info("shutting down embedded DCGM")

	e.cleanup()
	e.wg.Wait()

	e.logger.Info("embedded DCGM shutdown complete")
	return nil
}

// Reconnect attempts to re-establish the DCGM connection.
// TODO(#163): Implement actual reconnection logic with exponential backoff.
// Currently returns ErrNotInitialized - callers should use Shutdown + Init.
func (e *Embedded) Reconnect(ctx context.Context) error {
	if !e.initialized.Load() {
		return ErrNotInitialized
	}
	// Already connected, no-op
	return nil
}

// IsAvailable returns true if DCGM is initialized and connected.
func (e *Embedded) IsAvailable() bool {
	return e.initialized.Load()
}

// WatchFields delegates to the client.
func (e *Embedded) WatchFields(gpuID int, fields []FieldID, interval time.Duration) error {
	if !e.initialized.Load() {
		return ErrNotInitialized
	}
	return e.client.WatchFields(gpuID, fields, interval)
}

// GetLatestValues delegates to the client.
func (e *Embedded) GetLatestValues(gpuID int, fields []FieldID) (map[FieldID]Value, error) {
	if !e.initialized.Load() {
		return nil, ErrNotInitialized
	}
	return e.client.GetLatestValues(gpuID, fields)
}

// GetProfilingMetrics delegates to the client.
func (e *Embedded) GetProfilingMetrics(gpuID int) (*ProfilingMetrics, error) {
	if !e.initialized.Load() {
		return nil, ErrNotInitialized
	}
	return e.client.GetProfilingMetrics(gpuID)
}

// GetNVSwitchStatus delegates to the client.
func (e *Embedded) GetNVSwitchStatus() (*NVSwitchStatus, error) {
	if !e.initialized.Load() {
		return nil, ErrNotInitialized
	}
	return e.client.GetNVSwitchStatus()
}

// SetHealthPolicy delegates to the client.
func (e *Embedded) SetHealthPolicy(policy HealthPolicy) error {
	if !e.initialized.Load() {
		return ErrNotInitialized
	}
	return e.client.SetHealthPolicy(policy)
}

// GetHealthViolations delegates to the client.
func (e *Embedded) GetHealthViolations() ([]HealthViolation, error) {
	if !e.initialized.Load() {
		return nil, ErrNotInitialized
	}
	return e.client.GetHealthViolations()
}

// GetXIDErrors delegates to the client.
func (e *Embedded) GetXIDErrors(gpuID int, since time.Time) ([]XIDError, error) {
	if !e.initialized.Load() {
		return nil, ErrNotInitialized
	}
	return e.client.GetXIDErrors(gpuID, since)
}
