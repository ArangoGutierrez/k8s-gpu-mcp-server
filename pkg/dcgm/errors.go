// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import "errors"

// DCGM errors.
var (
	// ErrDCGMUnavailable indicates DCGM is not available on this system.
	// The agent will operate in NVML-only mode.
	ErrDCGMUnavailable = errors.New("dcgm: unavailable (NVML-only mode)")

	// ErrNotInitialized indicates DCGM has not been initialized.
	// Call Init() before using other methods.
	ErrNotInitialized = errors.New("dcgm: not initialized")

	// ErrAlreadyInitialized indicates Init() was called multiple times.
	ErrAlreadyInitialized = errors.New("dcgm: already initialized")

	// ErrInvalidMode indicates an invalid DCGM mode was specified.
	// Valid modes are "embedded" and "external".
	ErrInvalidMode = errors.New("dcgm: invalid mode (must be 'embedded' or 'external')")

	// ErrSocketRequired indicates a socket path is required for external mode.
	ErrSocketRequired = errors.New("dcgm: socket path required for external mode")

	// ErrSocketNotFound indicates the DCGM socket was not found.
	ErrSocketNotFound = errors.New("dcgm: socket not found")

	// ErrIntervalTooShort indicates the watch interval is too short.
	// Minimum interval is 100ms to prevent excessive CPU usage.
	ErrIntervalTooShort = errors.New("dcgm: watch interval must be >= 100ms")

	// ErrInvalidGPU indicates an invalid GPU index was provided.
	ErrInvalidGPU = errors.New("dcgm: invalid GPU index")

	// ErrHostengineStartFailed indicates nv-hostengine failed to start.
	// Check that nv-hostengine binary is present and executable.
	ErrHostengineStartFailed = errors.New("dcgm: nv-hostengine failed to start")

	// ErrHostengineNotFound indicates nv-hostengine binary was not found.
	ErrHostengineNotFound = errors.New("dcgm: nv-hostengine not found in PATH")

	// ErrConnectionFailed indicates DCGM connection failed.
	ErrConnectionFailed = errors.New("dcgm: connection failed")

	// ErrConnectionLost indicates the DCGM connection was lost.
	ErrConnectionLost = errors.New("dcgm: connection lost")

	// ErrProfilingUnavailable indicates profiling metrics are not available.
	// Profiling requires datacenter-class GPUs with profiling support.
	ErrProfilingUnavailable = errors.New("dcgm: profiling unavailable (requires datacenter GPU)")

	// ErrNVSwitchUnavailable indicates NVSwitch is not available.
	// NVSwitch is only present in certain GPU configurations.
	ErrNVSwitchUnavailable = errors.New("dcgm: nvswitch unavailable")

	// ErrFieldNotSupported indicates the requested field is not supported.
	ErrFieldNotSupported = errors.New("dcgm: field not supported")

	// ErrHealthPolicyInvalid indicates an invalid health policy configuration.
	ErrHealthPolicyInvalid = errors.New("dcgm: invalid health policy")
)
