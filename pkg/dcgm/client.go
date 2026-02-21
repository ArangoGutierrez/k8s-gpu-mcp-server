// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

//go:build !dcgm_cgo

// Build constraint: This file provides a placeholder DCGM client implementation.
// When real DCGM CGO bindings are added (with "dcgm_cgo" build tag), this file
// will be excluded and replaced by the actual implementation.
//
// To build with real DCGM bindings (when available):
//   go build -tags dcgm_cgo ./...
//
// Default build (placeholder, no CGO required):
//   go build ./...

package dcgm

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Client wraps the DCGM API for field subscriptions and queries.
// This implementation provides the interface but requires actual DCGM
// bindings (via CGO or go-dcgm library) for production use.
//
// TODO(#163): This is a placeholder implementation. Data methods return
// placeholder values or ErrProfilingUnavailable. When DCGM CGO bindings
// are added, replace the placeholder implementations with actual DCGM API calls.
// See: https://github.com/NVIDIA/go-dcgm for reference bindings.
type Client struct {
	endpoint string
	logger   *slog.Logger

	mu sync.RWMutex

	// Connection state
	connected atomic.Bool

	// Field subscriptions (gpuID -> fields)
	watchers map[int][]FieldID

	// Cached values (gpuID -> fieldID -> value)
	cache map[int]map[FieldID]Value
}

// NewClient creates a new DCGM client.
func NewClient(endpoint string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		endpoint: endpoint,
		logger:   logger,
		watchers: make(map[int][]FieldID),
		cache:    make(map[int]map[FieldID]Value),
	}
}

// Connect establishes connection to DCGM.
// This is a placeholder - actual implementation would use DCGM bindings.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected.Load() {
		return nil
	}

	c.logger.Debug("connecting to DCGM",
		"endpoint", c.endpoint,
	)

	// TODO(#163): Implement actual DCGM connection using CGO bindings.
	// This would typically involve:
	// 1. dcgm.Init() or dcgm.Connect(endpoint)
	// 2. Verify connection with a simple query
	//
	// For now, we simulate a successful connection.
	// In production, if DCGM bindings are not available,
	// this should return ErrDCGMUnavailable.

	c.connected.Store(true)
	c.logger.Info("connected to DCGM",
		"endpoint", c.endpoint,
	)

	return nil
}

// Disconnect closes the DCGM connection.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected.Load() {
		return nil
	}

	c.logger.Debug("disconnecting from DCGM")

	// TODO(#163): Implement actual DCGM disconnection.
	// dcgm.Shutdown()

	c.connected.Store(false)
	c.watchers = make(map[int][]FieldID)
	c.cache = make(map[int]map[FieldID]Value)

	return nil
}

// Reconnect attempts to re-establish the DCGM connection.
// TODO(#163): Implement actual reconnection with exponential backoff.
// Currently returns ErrNotInitialized - callers should use Disconnect + Connect.
func (c *Client) Reconnect(ctx context.Context) error {
	if !c.connected.Load() {
		return ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}
	// Already connected, no-op
	return nil
}

// IsConnected returns true if connected to DCGM.
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// WatchFields subscribes to field updates for a GPU.
func (c *Client) WatchFields(gpuID int, fields []FieldID, interval time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected.Load() {
		return ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}

	if interval < 100*time.Millisecond {
		return ErrIntervalTooShort
	}

	c.logger.Debug("watching fields",
		"gpu_id", gpuID,
		"fields", len(fields),
		"interval", interval,
	)

	// TODO(#163): Implement actual DCGM field watching with background polling.
	// This would typically involve:
	// 1. dcgm.WatchFields(gpuID, fields, interval)
	// 2. Start background goroutine to poll and cache values
	//
	// For now, just record the subscription (no actual watching occurs).
	c.watchers[gpuID] = append([]FieldID{}, fields...)

	// Initialize cache for this GPU if needed
	if c.cache[gpuID] == nil {
		c.cache[gpuID] = make(map[FieldID]Value)
	}

	return nil
}

// GetLatestValues returns the most recent values for specified fields.
func (c *Client) GetLatestValues(gpuID int, fields []FieldID) (map[FieldID]Value, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected.Load() {
		return nil, ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}

	// TODO(#163): Implement actual DCGM value retrieval.
	// This would typically involve:
	// 1. dcgm.GetLatestValues(gpuID, fields)
	// 2. Convert DCGM response to our Value type
	//
	// For now, return values from cache or not-supported status.
	result := make(map[FieldID]Value)
	gpuCache := c.cache[gpuID]

	now := time.Now()
	for _, field := range fields {
		if gpuCache != nil {
			if v, ok := gpuCache[field]; ok {
				v.Timestamp = now
				result[field] = v
				continue
			}
		}

		// Field not in cache, return not-supported
		result[field] = Value{
			FieldID:   field,
			Timestamp: now,
			Status:    ValueStatusNotSupported,
		}
	}

	return result, nil
}

// GetProfilingMetrics returns profiling metrics for a GPU.
func (c *Client) GetProfilingMetrics(gpuID int) (*ProfilingMetrics, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected.Load() {
		return nil, ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}

	// TODO(#163): Implement actual DCGM profiling query.
	// This requires:
	// 1. GPU supports profiling (datacenter class)
	// 2. Profiling fields are being watched
	// 3. Query DCGM_FI_PROF_* fields
	//
	// TODO(#163): placeholder — return unavailable until real DCGM bindings.
	return nil, ErrProfilingUnavailable
}

// GetNVSwitchStatus returns NVSwitch status.
func (c *Client) GetNVSwitchStatus() (*NVSwitchStatus, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected.Load() {
		return nil, ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}

	// TODO(#163): Implement actual NVSwitch query.
	// This requires:
	// 1. NVSwitch hardware present
	// 2. dcgm.GetNvSwitchStatus()
	//
	// TODO(#163): placeholder — return not available until real DCGM bindings.
	return &NVSwitchStatus{Available: false}, nil
}

// SetHealthPolicy configures a health policy.
func (c *Client) SetHealthPolicy(policy HealthPolicy) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected.Load() {
		return ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}

	if err := policy.Validate(); err != nil {
		return err
	}

	c.logger.Debug("setting health policy",
		"name", policy.Name,
		"field", policy.Field,
		"threshold", policy.Threshold,
	)

	// TODO(#163): Implement actual DCGM health policy.
	// dcgm.SetHealthPolicy(policy)
	//
	// For now, just accept and log.
	return nil
}

// GetHealthViolations returns health policy violations.
func (c *Client) GetHealthViolations() ([]HealthViolation, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected.Load() {
		return nil, ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}

	// TODO(#163): Implement actual DCGM health check.
	// dcgm.GetHealthViolations()
	//
	// TODO(#163): placeholder — return empty until real DCGM bindings.
	return []HealthViolation{}, nil
}

// GetXIDErrors returns XID errors from DCGM.
func (c *Client) GetXIDErrors(gpuID int, since time.Time) ([]XIDError, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected.Load() {
		return nil, ErrNotInitialized // TODO(#163): placeholder — no real DCGM bindings
	}

	// TODO(#163): Implement actual DCGM XID query.
	// Uses DCGM_FI_DEV_XID_ERRORS field.
	// This provides native XID detection without /dev/kmsg parsing.
	//
	// TODO(#163): placeholder — return empty until real DCGM bindings.
	return []XIDError{}, nil
}

// SetCachedValue allows manually setting a cached value (for testing).
func (c *Client) SetCachedValue(gpuID int, field FieldID, value Value) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cache[gpuID] == nil {
		c.cache[gpuID] = make(map[FieldID]Value)
	}
	c.cache[gpuID][field] = value
}

// Endpoint returns the DCGM endpoint.
func (c *Client) Endpoint() string {
	return c.endpoint
}

// IsPlaceholder returns true if this is the placeholder implementation.
// This allows callers to detect at runtime whether they're using the
// placeholder client (no real DCGM) or actual CGO bindings.
//
// Usage:
//
//	if client.IsPlaceholder() {
//	    logger.Warn("using placeholder DCGM client, profiling unavailable")
//	}
//
// When real DCGM CGO bindings are added (built with -tags dcgm_cgo),
// that implementation should return false.
func (c *Client) IsPlaceholder() bool {
	return true
}
