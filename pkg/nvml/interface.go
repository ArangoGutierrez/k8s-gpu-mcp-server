// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package nvml provides an abstraction layer over NVIDIA NVML library.
// This allows for testing without real hardware and decouples the
// application from the CGO-based NVML implementation.
package nvml

import (
	"context"
)

// Interface defines the contract for NVML operations.
// This interface can be implemented by both real NVML bindings
// and mock implementations for testing.
type Interface interface {
	// Init initializes the NVML library.
	// Must be called before any other NVML operations.
	Init(ctx context.Context) error

	// Shutdown shuts down the NVML library.
	// Should be called when done using NVML.
	Shutdown(ctx context.Context) error

	// GetDeviceCount returns the number of GPU devices.
	GetDeviceCount(ctx context.Context) (int, error)

	// GetDeviceByIndex returns a Device handle for the given index.
	GetDeviceByIndex(ctx context.Context, idx int) (Device, error)

	// GetDriverVersion returns the NVIDIA driver version string.
	GetDriverVersion(ctx context.Context) (string, error)

	// GetCudaDriverVersion returns the CUDA driver version as a string
	// (e.g., "12.9"). The raw version is major*1000 + minor*10.
	GetCudaDriverVersion(ctx context.Context) (string, error)
}

// Device represents a single GPU device.
type Device interface {
	// GetName returns the product name of the device.
	GetName(ctx context.Context) (string, error)

	// GetUUID returns the globally unique identifier of the device.
	GetUUID(ctx context.Context) (string, error)

	// GetPCIInfo returns PCI bus information for the device.
	GetPCIInfo(ctx context.Context) (*PCIInfo, error)

	// GetMemoryInfo returns memory usage information.
	GetMemoryInfo(ctx context.Context) (*MemoryInfo, error)

	// GetTemperature returns the current temperature in Celsius.
	GetTemperature(ctx context.Context) (uint32, error)

	// GetPowerUsage returns the current power usage in milliwatts.
	GetPowerUsage(ctx context.Context) (uint32, error)

	// GetUtilizationRates returns GPU and memory utilization rates.
	GetUtilizationRates(ctx context.Context) (*Utilization, error)

	// GetPowerManagementLimit returns the power management limit in
	// milliwatts. This is the maximum power the GPU is allowed to draw.
	GetPowerManagementLimit(ctx context.Context) (uint32, error)

	// GetEccMode returns whether ECC is currently enabled and pending mode.
	// Returns (current, pending, error). If ECC is not supported, returns
	// (false, false, nil).
	GetEccMode(ctx context.Context) (current, pending bool, err error)

	// GetTotalEccErrors returns the total count of ECC errors.
	// errorType: EccErrorCorrectable (0) or EccErrorUncorrectable (1).
	// If ECC is not supported, returns 0 with no error.
	GetTotalEccErrors(ctx context.Context, errorType int) (uint64, error)

	// GetCurrentClocksThrottleReasons returns a bitmask of current throttle
	// reasons. See ThrottleReason constants for bit definitions.
	// If not supported, returns 0 with no error.
	GetCurrentClocksThrottleReasons(ctx context.Context) (uint64, error)

	// GetClockInfo returns the current clock frequency in MHz for the given
	// clock type. clockType: ClockGraphics (0) or ClockMemory (1).
	GetClockInfo(ctx context.Context, clockType int) (uint32, error)

	// GetTemperatureThreshold returns the temperature threshold in Celsius.
	// thresholdType: TempThresholdShutdown (0) or TempThresholdSlowdown (1).
	// If not supported, returns 0 with no error.
	GetTemperatureThreshold(ctx context.Context, thresholdType int) (uint32, error)

	// GetCudaComputeCapability returns the CUDA compute capability as a
	// string (e.g., "7.5" for Turing, "8.0" for Ampere).
	GetCudaComputeCapability(ctx context.Context) (string, error)

	// GetNvLinkState returns whether the specified NVLink is active.
	// link: NVLink index (0 to MaxNvLinks-1 for the device).
	// Returns false with nil error if NVLink is not supported.
	GetNvLinkState(ctx context.Context, link int) (bool, error)

	// GetNvLinkRemotePciInfo returns PCI info of the device at the remote
	// end of the specified NVLink.
	// Returns nil with nil error if NVLink is not supported or not connected.
	GetNvLinkRemotePciInfo(ctx context.Context, link int) (*PCIInfo, error)

	// GetNvLinkErrorCounter returns the error count for the specified link
	// and counter type.
	// counterType: NvLinkErrorDL, NvLinkErrorReplay, NvLinkErrorRecovery, etc.
	// Returns 0 with nil error if NVLink is not supported.
	GetNvLinkErrorCounter(ctx context.Context, link int, counterType int) (uint64, error)
}

// PCIInfo contains PCI bus information for a device.
type PCIInfo struct {
	// BusID is the PCI bus ID (e.g., "0000:01:00.0")
	BusID string
	// Domain is the PCI domain
	Domain uint32
	// Bus is the PCI bus number
	Bus uint32
	// Device is the PCI device number
	Device uint32
}

// MemoryInfo contains memory usage information.
type MemoryInfo struct {
	// Total memory in bytes
	Total uint64
	// Used memory in bytes
	Used uint64
	// Free memory in bytes
	Free uint64
}

// Utilization contains GPU and memory utilization rates.
type Utilization struct {
	// GPU utilization rate (0-100%)
	GPU uint32
	// Memory utilization rate (0-100%)
	Memory uint32
}

// GPUInfo is a consolidated view of GPU device information.
type GPUInfo struct {
	Index             int    `json:"index"`
	Name              string `json:"name"`
	UUID              string `json:"uuid"`
	BusID             string `json:"bus_id"`
	ComputeCapability string `json:"compute_capability,omitempty"`

	// Memory information
	Memory MemorySpec `json:"memory"`

	// Temperature with thresholds
	Temperature TempSpec `json:"temperature"`

	// Power with limits
	Power PowerSpec `json:"power"`

	// Clock frequencies
	Clocks ClockSpec `json:"clocks"`

	// Utilization rates
	Utilization UtilSpec `json:"utilization"`

	// ECC status (nil if not supported)
	ECC *ECCSpec `json:"ecc,omitempty"`
}

// MemorySpec contains memory capacity information.
type MemorySpec struct {
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

// TempSpec contains temperature with thresholds.
type TempSpec struct {
	CurrentCelsius  uint32 `json:"current_celsius"`
	SlowdownCelsius uint32 `json:"slowdown_celsius"`
	ShutdownCelsius uint32 `json:"shutdown_celsius"`
}

// PowerSpec contains power usage and limits.
type PowerSpec struct {
	CurrentMW uint32 `json:"current_mw"`
	LimitMW   uint32 `json:"limit_mw"`
}

// ClockSpec contains clock frequencies.
type ClockSpec struct {
	SMMHZ     uint32 `json:"sm_mhz"`
	MemoryMHZ uint32 `json:"memory_mhz"`
}

// UtilSpec contains utilization rates.
type UtilSpec struct {
	GPUPercent    uint32 `json:"gpu_percent"`
	MemoryPercent uint32 `json:"memory_percent"`
}

// ECCSpec contains ECC memory status.
type ECCSpec struct {
	Enabled             bool   `json:"enabled"`
	CorrectableErrors   uint64 `json:"correctable_errors"`
	UncorrectableErrors uint64 `json:"uncorrectable_errors"`
}

// ThrottleReason constants for interpreting GetCurrentClocksThrottleReasons.
// These are bitmask values that can be combined.
const (
	ThrottleReasonGpuIdle            uint64 = 0x0000000000000001
	ThrottleReasonApplicationsClocks uint64 = 0x0000000000000002
	ThrottleReasonSwPowerCap         uint64 = 0x0000000000000004
	ThrottleReasonHwSlowdown         uint64 = 0x0000000000000008
	ThrottleReasonSyncBoost          uint64 = 0x0000000000000010
	ThrottleReasonSwThermalSlowdown  uint64 = 0x0000000000000020
	ThrottleReasonHwThermalSlowdown  uint64 = 0x0000000000000040
	ThrottleReasonHwPowerBrake       uint64 = 0x0000000000000080
)

// ClockType constants for GetClockInfo.
const (
	ClockGraphics = 0 // SM/Graphics clock
	ClockMemory   = 1 // Memory clock
)

// TemperatureThresholdType constants for GetTemperatureThreshold.
const (
	TempThresholdShutdown = 0 // GPU shutdown temperature
	TempThresholdSlowdown = 1 // GPU slowdown/throttle temperature
)

// EccErrorType constants for GetTotalEccErrors.
const (
	EccErrorCorrectable   = 0 // Single-bit correctable errors
	EccErrorUncorrectable = 1 // Double-bit uncorrectable errors
)

// NVLink constants.
const (
	// MaxNvLinks is the maximum number of NVLink connections per GPU.
	// Varies by architecture: A100=12, V100=6, consumer GPUs=0.
	MaxNvLinks = 12
)

// NVLink error counter types for GetNvLinkErrorCounter.
const (
	NvLinkErrorDL       = 0 // Data link transmit/receive errors
	NvLinkErrorReplay   = 1 // Replay errors (retransmissions)
	NvLinkErrorRecovery = 2 // Recovery count
	NvLinkErrorCRCFlit  = 3 // CRC errors on flits
	NvLinkErrorCRCData  = 4 // CRC errors on data
)
