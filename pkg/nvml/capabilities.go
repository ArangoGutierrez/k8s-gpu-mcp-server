// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package nvml

// CapabilityTier represents the level of NVML functionality available.
// Higher tiers include all capabilities of lower tiers.
type CapabilityTier int

const (
	// TierUnknown indicates capability detection failed or wasn't performed.
	TierUnknown CapabilityTier = iota
	// Tier1Basic supports: name, UUID, memory, temperature, power (Driver 450+)
	Tier1Basic
	// Tier2Health adds: ECC, throttling, clocks, temp thresholds (Driver 510+)
	Tier2Health
	// Tier3Advanced adds: NVLink topology/errors, compute processes (Driver 535+)
	Tier3Advanced
)

// String returns a human-readable tier name.
func (t CapabilityTier) String() string {
	switch t {
	case Tier1Basic:
		return "basic"
	case Tier2Health:
		return "health"
	case Tier3Advanced:
		return "advanced"
	default:
		return "unknown"
	}
}

// Capabilities describes the NVML features available on this system.
type Capabilities struct {
	// Tier is the overall capability level.
	Tier CapabilityTier `json:"tier"`
	// DriverVersion is the NVIDIA driver version string.
	DriverVersion string `json:"driver_version"`
	// CudaVersion is the CUDA driver version string.
	CudaVersion string `json:"cuda_version,omitempty"`
	// SupportedAPIs lists APIs that passed probing.
	SupportedAPIs []string `json:"supported_apis,omitempty"`
	// UnsupportedAPIs lists APIs that failed probing.
	UnsupportedAPIs []string `json:"unsupported_apis,omitempty"`
}

// API names for capability probing results.
const (
	APIName            = "name"
	APIUUID            = "uuid"
	APIPCIInfo         = "pci_info"
	APIMemoryInfo      = "memory_info"
	APITemperature     = "temperature"
	APIPowerUsage      = "power_usage"
	APIUtilization     = "utilization"
	APIPowerLimit      = "power_limit"
	APIEccMode         = "ecc_mode"
	APIEccErrors       = "ecc_errors"
	APIThrottleReasons = "throttle_reasons"
	APIClockInfo       = "clock_info"
	APITempThreshold   = "temp_threshold"
	// APIComputeCapability is probed for informational purposes but does not
	// affect tier calculation (always available on supported drivers).
	APIComputeCapability = "compute_capability"
	APINVLinkState       = "nvlink_state"
	APINVLinkRemotePCI   = "nvlink_remote_pci"
	APINVLinkErrors      = "nvlink_errors"
	APIComputeProcesses  = "compute_processes"
)

// Tier1APIs are the basic APIs required for Tier1Basic.
var Tier1APIs = []string{
	APIName, APIUUID, APIPCIInfo, APIMemoryInfo,
	APITemperature, APIPowerUsage, APIUtilization,
}

// Tier2APIs are the health monitoring APIs for Tier2Health.
var Tier2APIs = []string{
	APIPowerLimit, APIEccMode, APIEccErrors,
	APIThrottleReasons, APIClockInfo, APITempThreshold,
}

// Tier3APIs are the advanced APIs for Tier3Advanced.
var Tier3APIs = []string{
	APINVLinkState, APINVLinkRemotePCI, APINVLinkErrors,
	APIComputeProcesses,
}

// calculateTier determines the capability tier from supported APIs.
func calculateTier(supported map[string]bool) CapabilityTier {
	// Check Tier 1 first (required base)
	tier1OK := true
	for _, api := range Tier1APIs {
		if !supported[api] {
			tier1OK = false
			break
		}
	}
	if !tier1OK {
		return TierUnknown
	}

	// Check Tier 2 (Tier1 + Tier2 APIs)
	tier2OK := true
	for _, api := range Tier2APIs {
		if !supported[api] {
			tier2OK = false
			break
		}
	}
	if !tier2OK {
		return Tier1Basic
	}

	// Check Tier 3 (Tier1 + Tier2 + Tier3 APIs)
	tier3OK := true
	for _, api := range Tier3APIs {
		if !supported[api] {
			tier3OK = false
			break
		}
	}
	if !tier3OK {
		return Tier2Health
	}

	return Tier3Advanced
}

// buildCapabilities constructs a Capabilities struct from probing results.
func buildCapabilities(
	supported map[string]bool,
	driverVersion, cudaVersion string,
) *Capabilities {
	caps := &Capabilities{
		DriverVersion:   driverVersion,
		CudaVersion:     cudaVersion,
		SupportedAPIs:   make([]string, 0),
		UnsupportedAPIs: make([]string, 0),
	}

	// Build supported/unsupported lists from all tiers
	allAPIs := make([]string, 0, len(Tier1APIs)+len(Tier2APIs)+len(Tier3APIs))
	allAPIs = append(allAPIs, Tier1APIs...)
	allAPIs = append(allAPIs, Tier2APIs...)
	allAPIs = append(allAPIs, Tier3APIs...)

	for _, api := range allAPIs {
		if supported[api] {
			caps.SupportedAPIs = append(caps.SupportedAPIs, api)
		} else {
			caps.UnsupportedAPIs = append(caps.UnsupportedAPIs, api)
		}
	}

	caps.Tier = calculateTier(supported)
	return caps
}

// SupportsAPI returns true if the given API is supported.
func (c *Capabilities) SupportsAPI(api string) bool {
	for _, a := range c.SupportedAPIs {
		if a == api {
			return true
		}
	}
	return false
}

// IsDegraded returns true if the system is not running at full capability.
func (c *Capabilities) IsDegraded() bool {
	return c.Tier < Tier3Advanced
}

// DegradedReason returns a human-readable explanation of degraded mode.
func (c *Capabilities) DegradedReason() string {
	if !c.IsDegraded() {
		return ""
	}
	switch c.Tier {
	case TierUnknown:
		return "capability detection failed; some APIs may not work"
	case Tier1Basic:
		return "driver " + c.DriverVersion +
			" supports basic metrics only; health monitoring limited"
	case Tier2Health:
		return "driver " + c.DriverVersion +
			" supports health monitoring; NVLink and process info unavailable"
	default:
		return "running in degraded mode"
	}
}
