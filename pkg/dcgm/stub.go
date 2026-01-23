// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"time"
)

// Stub is a no-op implementation of Interface for when DCGM is unavailable.
// All data methods return ErrDCGMUnavailable.
// This allows the agent to operate in NVML-only mode.
type Stub struct{}

// Compile-time interface check.
var _ Interface = (*Stub)(nil)

// NewStub creates a new stub DCGM implementation.
// Use this when DCGM is not available or disabled.
func NewStub() *Stub {
	return &Stub{}
}

// Init is a no-op for stub.
func (s *Stub) Init(ctx context.Context) error {
	return nil
}

// Shutdown is a no-op for stub.
func (s *Stub) Shutdown(ctx context.Context) error {
	return nil
}

// Reconnect returns ErrDCGMUnavailable for stub.
func (s *Stub) Reconnect(ctx context.Context) error {
	return ErrDCGMUnavailable
}

// IsAvailable always returns false for stub.
func (s *Stub) IsAvailable() bool {
	return false
}

// WatchFields returns ErrDCGMUnavailable.
func (s *Stub) WatchFields(gpuID int, fields []FieldID, interval time.Duration) error {
	return ErrDCGMUnavailable
}

// GetLatestValues returns ErrDCGMUnavailable.
func (s *Stub) GetLatestValues(gpuID int, fields []FieldID) (map[FieldID]Value, error) {
	return nil, ErrDCGMUnavailable
}

// GetProfilingMetrics returns ErrDCGMUnavailable.
func (s *Stub) GetProfilingMetrics(gpuID int) (*ProfilingMetrics, error) {
	return nil, ErrDCGMUnavailable
}

// GetNVSwitchStatus returns ErrDCGMUnavailable.
func (s *Stub) GetNVSwitchStatus() (*NVSwitchStatus, error) {
	return nil, ErrDCGMUnavailable
}

// SetHealthPolicy returns ErrDCGMUnavailable.
func (s *Stub) SetHealthPolicy(policy HealthPolicy) error {
	return ErrDCGMUnavailable
}

// GetHealthViolations returns ErrDCGMUnavailable.
func (s *Stub) GetHealthViolations() ([]HealthViolation, error) {
	return nil, ErrDCGMUnavailable
}

// GetXIDErrors returns ErrDCGMUnavailable.
func (s *Stub) GetXIDErrors(gpuID int, since time.Time) ([]XIDError, error) {
	return nil, ErrDCGMUnavailable
}
