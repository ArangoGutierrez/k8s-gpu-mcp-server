// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package blackbox

import (
	"time"
)

// GPUSnapshot captures the state of a GPU at a point in time.
// This is the primary data structure stored in the ring buffer.
type GPUSnapshot struct {
	// Timestamp when this snapshot was captured.
	Timestamp time.Time `json:"timestamp"`

	// Index is the GPU device index (0-based).
	Index int `json:"index"`

	// UUID is the globally unique identifier for this GPU.
	UUID string `json:"uuid"`

	// Temperature is the current GPU temperature in Celsius.
	Temperature uint32 `json:"temperature_celsius"`

	// TempThreshold is the slowdown temperature threshold in Celsius.
	TempThreshold uint32 `json:"temp_threshold_celsius"`

	// PowerMW is the current power draw in milliwatts.
	PowerMW uint32 `json:"power_mw"`

	// PowerLimitMW is the power management limit in milliwatts.
	PowerLimitMW uint32 `json:"power_limit_mw"`

	// MemUsed is the used GPU memory in bytes.
	MemUsed uint64 `json:"mem_used_bytes"`

	// MemTotal is the total GPU memory in bytes.
	MemTotal uint64 `json:"mem_total_bytes"`

	// GPUUtil is the GPU utilization percentage (0-100).
	GPUUtil uint32 `json:"gpu_util_percent"`

	// MemUtil is the memory utilization percentage (0-100).
	MemUtil uint32 `json:"mem_util_percent"`

	// SMClock is the current SM/Graphics clock in MHz.
	SMClock uint32 `json:"sm_clock_mhz"`

	// MemClock is the current memory clock in MHz.
	MemClock uint32 `json:"mem_clock_mhz"`

	// Throttling is a bitmask of current throttle reasons.
	// See nvml.ThrottleReason* constants for bit definitions.
	Throttling uint64 `json:"throttle_reasons"`

	// ECCCorrectable is the count of correctable (single-bit) ECC errors.
	ECCCorrectable uint64 `json:"ecc_correctable"`

	// ECCUncorrectable is the count of uncorrectable (double-bit) ECC errors.
	ECCUncorrectable uint64 `json:"ecc_uncorrectable"`

	// Processes contains information about processes using this GPU.
	// May be empty if process tracking is disabled.
	Processes []ProcessInfo `json:"processes,omitempty"`
}

// ProcessInfo contains information about a process using a GPU.
type ProcessInfo struct {
	// PID is the process ID.
	PID int `json:"pid"`

	// UsedGPUMemory is the GPU memory used by this process in bytes.
	UsedGPUMemory uint64 `json:"used_gpu_memory_bytes,omitempty"`

	// PodUID is the Kubernetes Pod UID (if mapped).
	PodUID string `json:"pod_uid,omitempty"`

	// PodName is the Kubernetes Pod name (if mapped).
	PodName string `json:"pod_name,omitempty"`

	// Namespace is the Kubernetes namespace (if mapped).
	Namespace string `json:"namespace,omitempty"`

	// ContainerName is the container name within the Pod (if mapped).
	ContainerName string `json:"container_name,omitempty"`
}

// GetTimestamp returns the timestamp of this snapshot.
// This method satisfies the Timestamped interface for RingBuffer queries.
func (s GPUSnapshot) GetTimestamp() time.Time {
	return s.Timestamp
}

// IsThrottled returns true if any throttle reasons are active.
func (s GPUSnapshot) IsThrottled() bool {
	return s.Throttling != 0
}

// HasECCErrors returns true if there are any ECC errors (correctable or not).
func (s GPUSnapshot) HasECCErrors() bool {
	return s.ECCCorrectable > 0 || s.ECCUncorrectable > 0
}

// MemoryUsagePercent returns memory usage as a percentage.
func (s GPUSnapshot) MemoryUsagePercent() float64 {
	if s.MemTotal == 0 {
		return 0
	}
	return float64(s.MemUsed) / float64(s.MemTotal) * 100
}

// PowerUsagePercent returns power usage as a percentage of the limit.
func (s GPUSnapshot) PowerUsagePercent() float64 {
	if s.PowerLimitMW == 0 {
		return 0
	}
	return float64(s.PowerMW) / float64(s.PowerLimitMW) * 100
}
