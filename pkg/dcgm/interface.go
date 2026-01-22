// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"time"
)

// Interface defines the contract for DCGM operations.
// This interface can be implemented by embedded mode, external socket mode,
// mock implementations for testing, or stub when DCGM is unavailable.
type Interface interface {
	// Init initializes the DCGM connection.
	// For embedded mode, this starts nv-hostengine.
	// For external mode, this connects to the socket.
	// Returns ErrAlreadyInitialized if called multiple times.
	Init(ctx context.Context) error

	// Shutdown gracefully closes the DCGM connection.
	// For embedded mode, this stops nv-hostengine.
	// Safe to call multiple times or if not initialized.
	Shutdown(ctx context.Context) error

	// Reconnect attempts to re-establish the DCGM connection after
	// ErrConnectionLost or other transient failures.
	// For embedded mode, this restarts nv-hostengine if needed.
	// For external mode, this reconnects to the socket.
	// Returns ErrNotInitialized if Init() was never called.
	// Safe to call if already connected (no-op).
	Reconnect(ctx context.Context) error

	// IsAvailable returns true if DCGM is available and initialized.
	// Use this to check before calling DCGM-specific methods.
	IsAvailable() bool

	// WatchFields subscribes to field updates for a GPU.
	// Fields are sampled at the specified interval and cached.
	// Returns ErrNotInitialized if Init() was not called.
	// Returns ErrInvalidGPU if gpuID is out of range.
	WatchFields(gpuID int, fields []FieldID, interval time.Duration) error

	// GetLatestValues returns the most recent values for specified fields.
	// Values are from the cache populated by WatchFields.
	// Returns ErrNotInitialized if Init() was not called.
	// Returns ErrInvalidGPU if gpuID is out of range.
	GetLatestValues(gpuID int, fields []FieldID) (map[FieldID]Value, error)

	// GetProfilingMetrics returns profiling metrics for a GPU.
	// Requires DCGM and profiling-capable GPU (datacenter class).
	// Returns ErrProfilingUnavailable if profiling is not supported.
	GetProfilingMetrics(gpuID int) (*ProfilingMetrics, error)

	// GetNVSwitchStatus returns NVSwitch fabric status.
	// Requires DCGM and NVSwitch-connected GPUs.
	// Returns NVSwitchStatus with Available=false if NVSwitch not present.
	GetNVSwitchStatus() (*NVSwitchStatus, error)

	// SetHealthPolicy configures a health monitoring policy.
	// Returns ErrHealthPolicyInvalid if the policy is invalid.
	SetHealthPolicy(policy HealthPolicy) error

	// GetHealthViolations returns current health policy violations.
	// Returns empty slice if no violations.
	GetHealthViolations() ([]HealthViolation, error)

	// GetXIDErrors returns XID errors from DCGM's native field.
	// This is more reliable than parsing /dev/kmsg.
	// Only returns errors since the specified time.
	GetXIDErrors(gpuID int, since time.Time) ([]XIDError, error)
}

// FieldID represents a DCGM field identifier.
// See DCGM documentation for complete field definitions.
// https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html
type FieldID uint32

// Common DCGM fields used by the agent.
// Field IDs match DCGM_FI_* constants from dcgm_fields.h.
const (
	// Basic telemetry (also available via NVML, but with DCGM aggregation).
	FieldGPUTemp    FieldID = 150 // DCGM_FI_DEV_GPU_TEMP
	FieldPowerUsage FieldID = 155 // DCGM_FI_DEV_POWER_USAGE
	FieldGPUUtil    FieldID = 203 // DCGM_FI_DEV_GPU_UTIL
	FieldMemUtil    FieldID = 204 // DCGM_FI_DEV_MEM_COPY_UTIL
	FieldMemUsed    FieldID = 251 // DCGM_FI_DEV_FB_USED
	FieldMemFree    FieldID = 252 // DCGM_FI_DEV_FB_FREE

	// ECC errors.
	FieldECCSBE FieldID = 310 // DCGM_FI_DEV_ECC_SBE_VOL_TOTAL (correctable)
	FieldECCDBE FieldID = 311 // DCGM_FI_DEV_ECC_DBE_VOL_TOTAL (uncorrectable)

	// XID errors (DCGM-exclusive, no log parsing needed).
	FieldXIDErrors FieldID = 230 // DCGM_FI_DEV_XID_ERRORS

	// Profiling metrics (DCGM-exclusive, datacenter GPUs only).
	FieldSMActive     FieldID = 1002 // DCGM_FI_PROF_SM_ACTIVE
	FieldSMOccupancy  FieldID = 1003 // DCGM_FI_PROF_SM_OCCUPANCY
	FieldTensorActive FieldID = 1004 // DCGM_FI_PROF_PIPE_TENSOR_ACTIVE
	FieldDRAMActive   FieldID = 1005 // DCGM_FI_PROF_DRAM_ACTIVE
	FieldFP64Active   FieldID = 1006 // DCGM_FI_PROF_PIPE_FP64_ACTIVE
	FieldFP32Active   FieldID = 1007 // DCGM_FI_PROF_PIPE_FP32_ACTIVE
	FieldFP16Active   FieldID = 1008 // DCGM_FI_PROF_PIPE_FP16_ACTIVE
	FieldPCIeTx       FieldID = 1009 // DCGM_FI_PROF_PCIE_TX_BYTES
	FieldPCIeRx       FieldID = 1010 // DCGM_FI_PROF_PCIE_RX_BYTES
	FieldNVLinkTx     FieldID = 1011 // DCGM_FI_PROF_NVLINK_TX_BYTES
	FieldNVLinkRx     FieldID = 1012 // DCGM_FI_PROF_NVLINK_RX_BYTES

	// NVLink per-link errors.
	FieldNVLinkCRCErrors    FieldID = 409 // DCGM_FI_DEV_NVLINK_CRC_FLIT_ERROR_COUNT_TOTAL
	FieldNVLinkReplayErrors FieldID = 419 // DCGM_FI_DEV_NVLINK_REPLAY_ERROR_COUNT_TOTAL
)

// DefaultWatchFields returns the recommended set of fields to watch.
// These provide good coverage for GPU health monitoring without
// excessive overhead.
func DefaultWatchFields() []FieldID {
	return []FieldID{
		FieldGPUTemp,
		FieldPowerUsage,
		FieldGPUUtil,
		FieldMemUtil,
		FieldMemUsed,
		FieldXIDErrors,
	}
}

// ProfilingWatchFields returns fields for detailed profiling.
// These require datacenter-class GPUs with profiling support.
func ProfilingWatchFields() []FieldID {
	return []FieldID{
		FieldSMActive,
		FieldSMOccupancy,
		FieldTensorActive,
		FieldDRAMActive,
		FieldFP64Active,
		FieldFP32Active,
		FieldFP16Active,
		FieldPCIeTx,
		FieldPCIeRx,
		FieldNVLinkTx,
		FieldNVLinkRx,
	}
}
