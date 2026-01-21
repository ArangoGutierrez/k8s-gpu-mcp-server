// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
)

// IncidentReport is the analyzed output from the Analyzer. It contains root
// cause analysis with confidence scoring and actionable recommendations
// derived from pattern matching against known failure modes.
type IncidentReport struct {
	// ID is the unique identifier for this incident (from CorrelatedIncident).
	ID string `json:"id"`

	// Timestamp is when the incident occurred.
	Timestamp time.Time `json:"timestamp"`

	// Pod contains information about the affected pod.
	Pod PodInfo `json:"pod,omitempty"`

	// Node is the node where the incident occurred.
	Node string `json:"node,omitempty"`

	// GPUUUID is the UUID of the affected GPU.
	GPUUUID string `json:"gpu_uuid,omitempty"`

	// RootCause contains the analysis result with confidence scoring.
	RootCause RootCause `json:"root_cause"`

	// Timeline is the chronological list of events from the incident.
	Timeline []events.TimelineEntry `json:"timeline"`

	// HardwareState captures GPU telemetry at the time of failure.
	HardwareState *HardwareSnapshot `json:"hardware_state_at_failure,omitempty"`

	// Recommendations are actionable steps to resolve or mitigate the issue.
	Recommendations []Recommendation `json:"recommendations"`
}

// RootCause contains the analyzed root cause with confidence scoring.
type RootCause struct {
	// Category is the identified failure pattern (e.g., "thermal_cascade",
	// "ecc_failure", "software_oom").
	Category string `json:"category"`

	// Confidence is the match score from 0.0 to 1.0.
	// Higher values indicate stronger pattern match.
	Confidence float64 `json:"confidence"`

	// Evidence lists specific observations supporting this diagnosis.
	Evidence []string `json:"evidence"`

	// NotYourCode indicates whether this is a hardware/infrastructure issue
	// (true) versus a user code/application issue (false).
	NotYourCode bool `json:"not_your_code"`
}

// Recommendation is an actionable step to address the incident.
type Recommendation struct {
	// Action is a human-readable description of what to do.
	Action string `json:"action"`

	// Command is an optional kubectl or shell command to execute.
	Command string `json:"command,omitempty"`

	// Priority indicates urgency: "high", "medium", or "low".
	Priority string `json:"priority"`
}

// PodInfo contains Kubernetes Pod identification.
type PodInfo struct {
	// Name is the Pod name.
	Name string `json:"name"`

	// Namespace is the Pod's namespace.
	Namespace string `json:"namespace"`

	// UID is the Pod's unique identifier.
	UID string `json:"uid,omitempty"`
}

// HardwareSnapshot captures GPU telemetry at failure time.
type HardwareSnapshot struct {
	// Temperature is the GPU temperature in Celsius.
	Temperature uint32 `json:"temperature"`

	// TempThreshold is the slowdown threshold in Celsius.
	TempThreshold uint32 `json:"temp_threshold,omitempty"`

	// MemUsed is the used GPU memory in bytes.
	MemUsed uint64 `json:"mem_used"`

	// MemTotal is the total GPU memory in bytes.
	MemTotal uint64 `json:"mem_total"`

	// ECCCorrectable is the count of correctable ECC errors.
	ECCCorrectable uint64 `json:"ecc_correctable"`

	// ECCUncorrectable is the count of uncorrectable ECC errors.
	ECCUncorrectable uint64 `json:"ecc_uncorrectable"`

	// Throttling is the bitmask of active throttle reasons.
	Throttling uint64 `json:"throttling"`
}

// MemoryUsagePercent returns memory usage as a percentage.
func (h *HardwareSnapshot) MemoryUsagePercent() float64 {
	if h == nil || h.MemTotal == 0 {
		return 0
	}
	return float64(h.MemUsed) / float64(h.MemTotal) * 100
}

// IsThrottled returns true if any throttle reasons are active.
func (h *HardwareSnapshot) IsThrottled() bool {
	if h == nil {
		return false
	}
	return h.Throttling != 0
}

// HasUncorrectableECC returns true if uncorrectable ECC errors exist.
func (h *HardwareSnapshot) HasUncorrectableECC() bool {
	if h == nil {
		return false
	}
	return h.ECCUncorrectable > 0
}
