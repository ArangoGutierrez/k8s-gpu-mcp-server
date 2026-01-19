// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package blackbox

import (
	"errors"
	"time"
)

// Default configuration values.
const (
	// DefaultInterval is the default sampling interval.
	DefaultInterval = 10 * time.Second

	// DefaultRetention is the default data retention period.
	DefaultRetention = 30 * time.Minute

	// MinInterval is the minimum allowed sampling interval.
	MinInterval = 1 * time.Second

	// MinRetention is the minimum allowed retention period.
	MinRetention = 1 * time.Minute

	// MaxRetention is the maximum allowed retention period (2 hours).
	MaxRetention = 2 * time.Hour
)

// Configuration errors.
var (
	ErrInvalidInterval  = errors.New("interval must be >= 1s")
	ErrInvalidRetention = errors.New("retention must be between 1m and 2h")
	ErrRetentionTooLow  = errors.New("retention must be >= interval")
)

// RecorderConfig holds configuration for the Flight Recorder.
type RecorderConfig struct {
	// Interval is the sampling interval. Default: 10s.
	// Must be >= 1s.
	Interval time.Duration

	// Retention is the data retention period. Default: 30m.
	// Must be between 1m and 2h, and >= Interval.
	Retention time.Duration

	// EnableProcesses enables tracking of processes using GPUs.
	// When enabled, each snapshot includes PID information.
	// Default: true.
	EnableProcesses bool
}

// DefaultConfig returns a RecorderConfig with sensible defaults.
//
//	Interval:        10s
//	Retention:       30m
//	EnableProcesses: true
func DefaultConfig() RecorderConfig {
	return RecorderConfig{
		Interval:        DefaultInterval,
		Retention:       DefaultRetention,
		EnableProcesses: true,
	}
}

// Validate checks the configuration for errors.
func (c RecorderConfig) Validate() error {
	if c.Interval < MinInterval {
		return ErrInvalidInterval
	}
	if c.Retention < MinRetention || c.Retention > MaxRetention {
		return ErrInvalidRetention
	}
	if c.Retention < c.Interval {
		return ErrRetentionTooLow
	}
	return nil
}

// BufferCapacity returns the number of snapshots to retain per GPU.
// This is calculated as Retention / Interval.
func (c RecorderConfig) BufferCapacity() int {
	if c.Interval <= 0 {
		return 0
	}
	return int(c.Retention / c.Interval)
}

// WithInterval returns a copy of the config with the given interval.
func (c RecorderConfig) WithInterval(d time.Duration) RecorderConfig {
	c.Interval = d
	return c
}

// WithRetention returns a copy of the config with the given retention.
func (c RecorderConfig) WithRetention(d time.Duration) RecorderConfig {
	c.Retention = d
	return c
}

// WithProcesses returns a copy with process tracking enabled/disabled.
func (c RecorderConfig) WithProcesses(enabled bool) RecorderConfig {
	c.EnableProcesses = enabled
	return c
}
