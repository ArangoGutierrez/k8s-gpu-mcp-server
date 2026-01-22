// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"fmt"
	"time"
)

// Value represents a DCGM field value with metadata.
type Value struct {
	// FieldID is the field this value corresponds to.
	FieldID FieldID `json:"field_id"`

	// Timestamp when this value was sampled.
	Timestamp time.Time `json:"timestamp"`

	// Status indicates the validity of the value.
	Status ValueStatus `json:"status"`

	// Int64 is the value for integer fields.
	Int64 int64 `json:"int64,omitempty"`

	// Float64 is the value for floating-point fields.
	Float64 float64 `json:"float64,omitempty"`

	// String is the value for string fields.
	String string `json:"string,omitempty"`
}

// ValueStatus represents the status of a DCGM value.
type ValueStatus int

// Value status constants matching DCGM status codes.
const (
	ValueStatusOK           ValueStatus = 0
	ValueStatusBlank        ValueStatus = 1 // Never been set
	ValueStatusStale        ValueStatus = 2 // Not updated recently
	ValueStatusNotFound     ValueStatus = 3 // Field not found
	ValueStatusNotSupported ValueStatus = 4 // Field not supported on this GPU
	ValueStatusError        ValueStatus = 5 // Error retrieving value
)

// String returns a human-readable status string.
func (s ValueStatus) String() string {
	switch s {
	case ValueStatusOK:
		return "ok"
	case ValueStatusBlank:
		return "blank"
	case ValueStatusStale:
		return "stale"
	case ValueStatusNotFound:
		return "not_found"
	case ValueStatusNotSupported:
		return "not_supported"
	case ValueStatusError:
		return "error"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// IsValid returns true if the value is usable.
func (v Value) IsValid() bool {
	return v.Status == ValueStatusOK
}

// ProfilingMetrics contains GPU profiling metrics only available via DCGM.
// These metrics require datacenter-class GPUs (Tesla, A100, H100, etc.).
type ProfilingMetrics struct {
	// GPUID is the GPU device index.
	GPUID int `json:"gpu_id"`

	// Timestamp when these metrics were sampled.
	Timestamp time.Time `json:"timestamp"`

	// SMActive is the percentage of time at least one warp is active (0-100).
	SMActive float64 `json:"sm_active_percent"`

	// SMOccupancy is the percentage of warps resident vs theoretical max (0-100).
	SMOccupancy float64 `json:"sm_occupancy_percent"`

	// TensorActivity is the percentage of time tensor cores are active (0-100).
	TensorActivity float64 `json:"tensor_activity_percent"`

	// DRAMActivity is the percentage of time DRAM interface is active (0-100).
	DRAMActivity float64 `json:"dram_activity_percent"`

	// FP64Activity is the percentage of time FP64 pipes are active (0-100).
	FP64Activity float64 `json:"fp64_activity_percent"`

	// FP32Activity is the percentage of time FP32 pipes are active (0-100).
	FP32Activity float64 `json:"fp32_activity_percent"`

	// FP16Activity is the percentage of time FP16 pipes are active (0-100).
	FP16Activity float64 `json:"fp16_activity_percent"`

	// PCIeTxBytes is the PCIe transmit throughput in bytes/sec.
	PCIeTxBytes uint64 `json:"pcie_tx_bytes_per_sec"`

	// PCIeRxBytes is the PCIe receive throughput in bytes/sec.
	PCIeRxBytes uint64 `json:"pcie_rx_bytes_per_sec"`

	// NVLinkTxBytes is the NVLink transmit throughput in bytes/sec.
	NVLinkTxBytes uint64 `json:"nvlink_tx_bytes_per_sec"`

	// NVLinkRxBytes is the NVLink receive throughput in bytes/sec.
	NVLinkRxBytes uint64 `json:"nvlink_rx_bytes_per_sec"`
}

// NVSwitchStatus represents the status of NVSwitch fabric.
type NVSwitchStatus struct {
	// Available indicates if NVSwitch is present in this system.
	Available bool `json:"available"`

	// SwitchCount is the number of NVSwitches detected.
	SwitchCount int `json:"switch_count"`

	// Switches contains per-switch status information.
	Switches []NVSwitchInfo `json:"switches,omitempty"`

	// Links contains NVSwitch link status for all links.
	Links []NVSwitchLink `json:"links,omitempty"`
}

// NVSwitchInfo contains information about a single NVSwitch.
type NVSwitchInfo struct {
	// ID is the switch identifier (index).
	ID int `json:"id"`

	// UUID is the globally unique identifier.
	UUID string `json:"uuid,omitempty"`

	// Status indicates the switch health.
	Status SwitchHealth `json:"status"`

	// Temperature in Celsius.
	Temperature uint32 `json:"temperature_celsius"`

	// FirmwareVersion is the NVSwitch firmware version.
	FirmwareVersion string `json:"firmware_version,omitempty"`
}

// SwitchHealth represents NVSwitch health status.
type SwitchHealth string

const (
	SwitchHealthHealthy  SwitchHealth = "healthy"
	SwitchHealthDegraded SwitchHealth = "degraded"
	SwitchHealthFailed   SwitchHealth = "failed"
	SwitchHealthUnknown  SwitchHealth = "unknown"
)

// NVSwitchLink represents a link in the NVSwitch fabric.
type NVSwitchLink struct {
	// SwitchID is the source switch index.
	SwitchID int `json:"switch_id"`

	// LinkID is the link index on the switch.
	LinkID int `json:"link_id"`

	// RemoteGPU is the GPU index connected via this link (-1 if not GPU).
	RemoteGPU int `json:"remote_gpu"`

	// RemoteSwitch is the switch index if connected to another switch (-1 if GPU).
	RemoteSwitch int `json:"remote_switch"`

	// State is the link state.
	State LinkState `json:"state"`

	// CRCErrors is the CRC error count on this link.
	CRCErrors uint64 `json:"crc_errors"`

	// ReplayErrors is the replay error count on this link.
	ReplayErrors uint64 `json:"replay_errors"`
}

// LinkState represents NVLink/NVSwitch link state.
type LinkState string

const (
	LinkStateActive   LinkState = "active"
	LinkStateInactive LinkState = "inactive"
	LinkStateError    LinkState = "error"
	LinkStateUnknown  LinkState = "unknown"
)

// HealthPolicy defines a health monitoring policy.
type HealthPolicy struct {
	// Name is the policy identifier.
	Name string `json:"name"`

	// Field is the DCGM field to monitor.
	Field FieldID `json:"field"`

	// Threshold is the value that triggers a violation.
	Threshold float64 `json:"threshold"`

	// Comparison is how to compare the value to threshold.
	Comparison Comparison `json:"comparison"`

	// Enabled indicates if the policy is active.
	Enabled bool `json:"enabled"`

	// GPUID is the specific GPU to monitor (-1 for all GPUs).
	GPUID int `json:"gpu_id"`
}

// Comparison defines how to compare a value to a threshold.
type Comparison string

const (
	ComparisonGreaterThan Comparison = "gt" // value > threshold
	ComparisonLessThan    Comparison = "lt" // value < threshold
	ComparisonEqual       Comparison = "eq" // value == threshold
	ComparisonNotEqual    Comparison = "ne" // value != threshold
)

// Validate checks if the health policy is valid.
func (p HealthPolicy) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("%w: name is required", ErrHealthPolicyInvalid)
	}
	switch p.Comparison {
	case ComparisonGreaterThan, ComparisonLessThan, ComparisonEqual, ComparisonNotEqual:
		// Valid
	default:
		return fmt.Errorf("%w: invalid comparison %q", ErrHealthPolicyInvalid, p.Comparison)
	}
	return nil
}

// HealthViolation represents a health policy violation.
type HealthViolation struct {
	// Policy is the violated policy name.
	Policy string `json:"policy"`

	// GPUID is the GPU that violated the policy.
	GPUID int `json:"gpu_id"`

	// Timestamp when the violation was detected.
	Timestamp time.Time `json:"timestamp"`

	// ActualValue is the value that caused the violation.
	ActualValue float64 `json:"actual_value"`

	// Threshold is the policy threshold that was exceeded.
	Threshold float64 `json:"threshold"`

	// Message is a human-readable description.
	Message string `json:"message"`
}

// XIDError represents an XID error from DCGM's native field.
type XIDError struct {
	// Timestamp when the error occurred.
	Timestamp time.Time `json:"timestamp"`

	// GPUID is the GPU that experienced the error.
	GPUID int `json:"gpu_id"`

	// Code is the XID error code.
	Code int `json:"code"`

	// Count is the number of occurrences since last query.
	Count int `json:"count"`
}

// Config holds DCGM configuration options.
type Config struct {
	// Enabled indicates if DCGM integration is enabled.
	Enabled bool `json:"enabled"`

	// Mode is the connection mode: "embedded" or "external".
	Mode string `json:"mode"`

	// Socket is the DCGM socket path for external mode.
	// Default: /var/run/dcgm.sock
	Socket string `json:"socket,omitempty"`

	// EmbeddedPort is the port for embedded nv-hostengine.
	// Default: 5555
	EmbeddedPort int `json:"embedded_port,omitempty"`

	// WatchInterval is the field sampling interval.
	// Default: 1s, minimum: 100ms
	WatchInterval time.Duration `json:"watch_interval,omitempty"`

	// Fields is the list of fields to watch.
	// Default: DefaultWatchFields()
	Fields []FieldID `json:"fields,omitempty"`

	// EnableProfiling enables profiling field collection.
	// Requires datacenter-class GPUs.
	EnableProfiling bool `json:"enable_profiling,omitempty"`
}

// DefaultConfig returns the default DCGM configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:         false,
		Mode:            "embedded",
		Socket:          "/var/run/dcgm.sock",
		EmbeddedPort:    5555,
		WatchInterval:   time.Second,
		Fields:          DefaultWatchFields(),
		EnableProfiling: false,
	}
}

// Validate checks if the configuration is valid.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil // Disabled config is always valid
	}

	if c.Mode != "embedded" && c.Mode != "external" {
		return ErrInvalidMode
	}

	if c.Mode == "external" && c.Socket == "" {
		return ErrSocketRequired
	}

	if c.WatchInterval < 100*time.Millisecond {
		return ErrIntervalTooShort
	}

	// Only validate EmbeddedPort for embedded mode
	if c.Mode == "embedded" && (c.EmbeddedPort <= 0 || c.EmbeddedPort > 65535) {
		return fmt.Errorf("dcgm: invalid embedded port %d", c.EmbeddedPort)
	}

	return nil
}
