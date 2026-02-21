// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/klog/v2"
)

// GPUHealthHandler handles the get_gpu_health tool.
type GPUHealthHandler struct {
	nvmlClient nvml.Interface
}

// NewGPUHealthHandler creates a new GPU health handler.
func NewGPUHealthHandler(nvmlClient nvml.Interface) *GPUHealthHandler {
	return &GPUHealthHandler{
		nvmlClient: nvmlClient,
	}
}

// GPUHealthResponse is the top-level response structure for GPU health status.
type GPUHealthResponse struct {
	Status         string            `json:"status"`
	OverallScore   int               `json:"overall_score"`
	DeviceCount    int               `json:"device_count"`
	HealthyCount   int               `json:"healthy_count"`
	WarningCount   int               `json:"warning_count"`
	DegradedCount  int               `json:"degraded_count"`
	CriticalCount  int               `json:"critical_count"`
	GPUs           []GPUHealthStatus `json:"gpus"`
	Recommendation string            `json:"recommendation"`
	// CapabilityTier indicates the NVML capability level (1=basic, 2=health,
	// 3=advanced). A value of 0 means capability detection failed.
	CapabilityTier int `json:"capability_tier"`
	// Degraded indicates if running with reduced NVML functionality due to
	// older driver or unsupported hardware features.
	Degraded bool `json:"degraded"`
	// DegradedReason explains why functionality is reduced. Empty when not
	// degraded.
	DegradedReason string `json:"degraded_reason,omitempty"`
}

// GPUHealthStatus contains health metrics for a single GPU.
type GPUHealthStatus struct {
	Index       int               `json:"index"`
	Name        string            `json:"name"`
	UUID        string            `json:"uuid"`
	PCIBusID    string            `json:"pci_bus_id"`
	Status      string            `json:"status"`
	HealthScore int               `json:"health_score"`
	Temperature TemperatureHealth `json:"temperature"`
	Memory      MemoryHealth      `json:"memory"`
	Power       PowerHealth       `json:"power"`
	Throttling  ThrottlingStatus  `json:"throttling"`
	ECCErrors   ECCHealth         `json:"ecc_errors"`
	Performance PerformanceHealth `json:"performance"`
	Issues      []HealthIssue     `json:"issues,omitempty"`
}

// TemperatureHealth tracks thermal status of the GPU.
type TemperatureHealth struct {
	Current   uint32 `json:"current_celsius"`
	Threshold uint32 `json:"threshold_celsius"`
	Max       uint32 `json:"max_celsius"`
	Status    string `json:"status"`
	Margin    int    `json:"margin_celsius"`
}

// MemoryHealth tracks memory usage patterns.
type MemoryHealth struct {
	Total       uint64  `json:"total_bytes"`
	Used        uint64  `json:"used_bytes"`
	Free        uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
	Status      string  `json:"status"`
}

// PowerHealth tracks power consumption relative to limits.
type PowerHealth struct {
	Current     uint32  `json:"current_mw"`
	Limit       uint32  `json:"limit_mw"`
	Default     uint32  `json:"default_mw"`
	UsedPercent float64 `json:"used_percent"`
	Status      string  `json:"status"`
}

// ThrottlingStatus indicates if GPU performance is being throttled.
type ThrottlingStatus struct {
	Active  bool     `json:"active"`
	Reasons []string `json:"reasons,omitempty"`
	Status  string   `json:"status"`
}

// ECCHealth tracks ECC memory error counts.
type ECCHealth struct {
	Enabled                  bool   `json:"enabled"`
	TotalCorrectableErrors   uint64 `json:"total_correctable_errors"`
	TotalUncorrectableErrors uint64 `json:"total_uncorrectable_errors"`
	Status                   string `json:"status"`
}

// PerformanceHealth tracks GPU utilization and clock frequencies.
type PerformanceHealth struct {
	GPUUtil     uint32 `json:"gpu_util_percent"`
	MemoryUtil  uint32 `json:"memory_util_percent"`
	SMClock     uint32 `json:"sm_clock_mhz"`
	MemoryClock uint32 `json:"memory_clock_mhz"`
	Status      string `json:"status"`
}

// HealthIssue describes a specific health concern with recommendations.
type HealthIssue struct {
	Severity   string `json:"severity"`
	Component  string `json:"component"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
}

// Handle processes the get_gpu_health tool request.
func (h *GPUHealthHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("get_gpu_health invoked")

	// Check context before starting
	if err := ctx.Err(); err != nil {
		klog.InfoS("context cancelled before health check")
		return mcp.NewToolResultError(
			fmt.Sprintf("operation cancelled: %s", err)), nil
	}

	// Get device count
	count, err := h.nvmlClient.GetDeviceCount(ctx)
	if err != nil {
		klog.ErrorS(err, "failed to get device count")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to get device count: %s", err)), nil
	}

	// Get capabilities for degraded mode reporting
	var caps *nvml.Capabilities
	if c, err := h.nvmlClient.GetCapabilities(ctx); err == nil && c != nil {
		caps = c
	}

	// Collect health for each GPU
	gpus := make([]GPUHealthStatus, 0, count)
	for i := 0; i < count; i++ {
		// Check for context cancellation
		if err := ctx.Err(); err != nil {
			klog.InfoS("context cancelled during enumeration")
			return mcp.NewToolResultError(
				fmt.Sprintf("operation cancelled: %s", err)), nil
		}

		device, err := h.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			klog.ErrorS(err, "failed to get device", "index", i)
			continue
		}
		if device == nil {
			klog.ErrorS(nil, "nil device returned without error", "index", i)
			continue
		}

		health := h.collectGPUHealth(ctx, i, device)
		gpus = append(gpus, health)
	}

	// Calculate overall status
	response := h.calculateOverallHealth(gpus)

	// Populate capability fields
	if caps != nil {
		response.CapabilityTier = int(caps.Tier)
		response.Degraded = caps.IsDegraded()
		response.DegradedReason = caps.DegradedReason()
	}

	// Generate recommendations
	response.Recommendation = h.generateRecommendation(response)

	klog.InfoS("get_gpu_health completed",
		"count", response.DeviceCount, "status", response.Status,
		"capability_tier", response.CapabilityTier,
		"degraded", response.Degraded)

	return h.marshalResponse(response)
}

// collectGPUHealth gathers health metrics for a single GPU device.
func (h *GPUHealthHandler) collectGPUHealth(
	ctx context.Context,
	index int,
	device nvml.Device,
) GPUHealthStatus {
	health := GPUHealthStatus{
		Index:  index,
		Issues: make([]HealthIssue, 0),
	}

	// Get basic info with warning logs on failure
	name, err := device.GetName(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get GPU name", "index", index, "error", err)
		health.Name = "Unknown"
	} else {
		health.Name = name
	}

	uuid, err := device.GetUUID(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get GPU UUID", "index", index, "error", err)
		health.UUID = "unknown"
	} else {
		health.UUID = uuid
	}

	pciInfo, err := device.GetPCIInfo(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get PCI info", "index", index, "error", err)
	} else {
		health.PCIBusID = pciInfo.BusID
	}

	// Check context before collecting metrics
	if err := ctx.Err(); err != nil {
		klog.V(4).InfoS("context cancelled during health collection")
		health.Status = "unknown"
		return health
	}

	// Collect metrics from each subsystem
	health.Temperature = h.checkTemperature(ctx, device)
	health.Memory = h.checkMemory(ctx, device)
	health.Power = h.checkPower(ctx, device)
	health.Throttling = h.checkThrottling(ctx, device)
	health.ECCErrors = h.checkECCErrors(ctx, device)
	health.Performance = h.checkPerformance(ctx, device)

	// Calculate health score (0-100)
	health.HealthScore = h.calculateHealthScore(&health)

	// Determine status based on score and issues
	health.Status = h.determineStatus(health.HealthScore, health.Issues)

	return health
}

// Temperature threshold constants.
// NOTE: These values are calibrated for NVIDIA Tesla T4 GPUs.
// Different GPU models have different thermal specifications:
// - Tesla T4: slowdown ~82°C, shutdown ~90°C
// - A100: slowdown ~83°C, shutdown ~92°C
// - V100: slowdown ~83°C, shutdown ~90°C
// TODO(#68): Add model-specific threshold detection via NVML when available.
const (
	// defaultTempThreshold is the temperature at which throttling begins.
	defaultTempThreshold uint32 = 82
	// defaultTempMax is the critical temperature limit.
	defaultTempMax uint32 = 90
	// tempElevatedMargin is the degrees below threshold for "elevated" status.
	// This provides early warning before thermal throttling occurs.
	tempElevatedMargin uint32 = 10
)

// checkTemperature evaluates GPU thermal status.
func (h *GPUHealthHandler) checkTemperature(
	ctx context.Context,
	device nvml.Device,
) TemperatureHealth {
	temp, err := device.GetTemperature(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get temperature", "error", err)
		return TemperatureHealth{
			Status: "unknown",
		}
	}

	// Get real thresholds from device, fallback to defaults
	threshold := defaultTempThreshold
	maxTemp := defaultTempMax

	if slowdown, err := device.GetTemperatureThreshold(
		ctx, nvml.TempThresholdSlowdown); err == nil && slowdown > 0 {
		threshold = slowdown
	}
	if shutdown, err := device.GetTemperatureThreshold(
		ctx, nvml.TempThresholdShutdown); err == nil && shutdown > 0 {
		maxTemp = shutdown
	}

	margin := int(threshold) - int(temp)

	var status string
	switch {
	case temp >= maxTemp:
		status = "critical"
	case temp >= threshold:
		status = "high"
	case temp >= threshold-tempElevatedMargin:
		status = "elevated"
	default:
		status = "normal"
	}

	return TemperatureHealth{
		Current:   temp,
		Threshold: threshold,
		Max:       maxTemp,
		Status:    status,
		Margin:    margin,
	}
}

// checkMemory evaluates GPU memory usage.
func (h *GPUHealthHandler) checkMemory(
	ctx context.Context,
	device nvml.Device,
) MemoryHealth {
	memInfo, err := device.GetMemoryInfo(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get memory info", "error", err)
		return MemoryHealth{
			Status: "unknown",
		}
	}

	var usedPercent float64
	if memInfo.Total > 0 {
		usedPercent = float64(memInfo.Used) / float64(memInfo.Total) * 100
	}

	var status string
	switch {
	case usedPercent >= 95:
		status = "critical"
	case usedPercent >= 90:
		status = "high"
	case usedPercent >= 80:
		status = "elevated"
	default:
		status = "normal"
	}

	return MemoryHealth{
		Total:       memInfo.Total,
		Used:        memInfo.Used,
		Free:        memInfo.Free,
		UsedPercent: usedPercent,
		Status:      status,
	}
}

// Power limit constants.
// NOTE: This default power limit (70W = 70000mW) is specific to NVIDIA Tesla
// T4 GPUs and is used here as a heuristic fallback only. For non-T4 GPUs,
// actual TDP and power limits can differ significantly:
// - Tesla T4: 70W TDP
// - A100 SXM: 400W TDP
// - V100 SXM2: 300W TDP
// - H100 SXM: 700W TDP
// The computed UsedPercent and Status values may not accurately reflect true
// power utilization for non-T4 GPUs.
// TODO(#69): Query actual device power management limit from NVML when available.
const defaultPowerLimit uint32 = 70000

// eccCorrectableThreshold is the lifetime count of single-bit ECC errors
// that triggers a warning. This threshold is based on industry practice:
// occasional correctable errors are normal, but high counts may indicate
// memory degradation. Value represents total lifetime errors, not rate.
const eccCorrectableThreshold uint64 = 1000

// checkPower evaluates GPU power consumption.
func (h *GPUHealthHandler) checkPower(
	ctx context.Context,
	device nvml.Device,
) PowerHealth {
	power, err := device.GetPowerUsage(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get power usage", "error", err)
		return PowerHealth{
			Status: "unknown",
		}
	}

	// Get real power limit from device, fallback to default
	limit := defaultPowerLimit
	if realLimit, err := device.GetPowerManagementLimit(ctx); err == nil &&
		realLimit > 0 {
		limit = realLimit
	}

	var usedPercent float64
	if limit > 0 {
		usedPercent = float64(power) / float64(limit) * 100
	}

	var status string
	switch {
	case usedPercent > 100:
		status = "over_limit"
	case usedPercent >= 95:
		status = "high"
	case usedPercent >= 80:
		status = "elevated"
	default:
		status = "normal"
	}

	return PowerHealth{
		Current:     power,
		Limit:       limit,
		Default:     defaultPowerLimit,
		UsedPercent: usedPercent,
		Status:      status,
	}
}

// checkThrottling evaluates GPU clock throttling status.
func (h *GPUHealthHandler) checkThrottling(
	ctx context.Context,
	device nvml.Device,
) ThrottlingStatus {
	reasons, err := device.GetCurrentClocksThrottleReasons(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get throttle reasons", "error", err)
		return ThrottlingStatus{
			Active:  false,
			Reasons: []string{},
			Status:  "unknown",
		}
	}

	// Ignore idle throttling (normal when GPU is not in use)
	activeReasons := reasons &^ nvml.ThrottleReasonGpuIdle

	if activeReasons == 0 {
		return ThrottlingStatus{
			Active:  false,
			Reasons: []string{},
			Status:  "none",
		}
	}

	// Parse throttle reasons into human-readable strings
	var reasonStrings []string
	if activeReasons&nvml.ThrottleReasonHwThermalSlowdown != 0 {
		reasonStrings = append(reasonStrings, "hw_thermal")
	}
	if activeReasons&nvml.ThrottleReasonSwThermalSlowdown != 0 {
		reasonStrings = append(reasonStrings, "sw_thermal")
	}
	if activeReasons&nvml.ThrottleReasonHwSlowdown != 0 {
		reasonStrings = append(reasonStrings, "hw_slowdown")
	}
	if activeReasons&nvml.ThrottleReasonSwPowerCap != 0 {
		reasonStrings = append(reasonStrings, "power_cap")
	}
	if activeReasons&nvml.ThrottleReasonHwPowerBrake != 0 {
		reasonStrings = append(reasonStrings, "power_brake")
	}
	if activeReasons&nvml.ThrottleReasonApplicationsClocks != 0 {
		reasonStrings = append(reasonStrings, "app_clocks")
	}
	if activeReasons&nvml.ThrottleReasonSyncBoost != 0 {
		reasonStrings = append(reasonStrings, "sync_boost")
	}

	// Determine severity based on reason count and type
	var status string
	hasThermal := activeReasons&(nvml.ThrottleReasonHwThermalSlowdown|
		nvml.ThrottleReasonSwThermalSlowdown) != 0

	if hasThermal || len(reasonStrings) >= 2 {
		status = "severe"
	} else {
		status = "minor"
	}

	return ThrottlingStatus{
		Active:  true,
		Reasons: reasonStrings,
		Status:  status,
	}
}

// checkECCErrors evaluates ECC memory error status.
func (h *GPUHealthHandler) checkECCErrors(
	ctx context.Context,
	device nvml.Device,
) ECCHealth {
	enabled, _, err := device.GetEccMode(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get ECC mode", "error", err)
		return ECCHealth{
			Enabled: false,
			Status:  "unknown",
		}
	}

	if !enabled {
		return ECCHealth{
			Enabled: false,
			Status:  "disabled",
		}
	}

	// Get error counts; report "unavailable" when query fails instead of
	// silently returning 0 which could mask GPU failures.
	var correctable, uncorrectable uint64
	correctableAvailable := true
	uncorrectableAvailable := true
	if val, err := device.GetTotalEccErrors(ctx, nvml.EccErrorCorrectable); err != nil {
		klog.V(2).InfoS("failed to get correctable ECC errors", "error", err)
		correctableAvailable = false
	} else {
		correctable = val
	}
	if val, err := device.GetTotalEccErrors(ctx, nvml.EccErrorUncorrectable); err != nil {
		klog.V(2).InfoS("failed to get uncorrectable ECC errors", "error", err)
		uncorrectableAvailable = false
	} else {
		uncorrectable = val
	}

	// If neither error count is available, report unknown status
	if !correctableAvailable && !uncorrectableAvailable {
		return ECCHealth{
			Enabled: true,
			Status:  "data_unavailable",
		}
	}

	// Determine status based on error counts
	var status string
	switch {
	case uncorrectableAvailable && uncorrectable > 0:
		status = "critical"
	case !uncorrectableAvailable:
		// Uncorrectable count unavailable — cannot confirm healthy
		status = "degraded"
	case correctableAvailable && correctable > eccCorrectableThreshold:
		status = "warning"
	case correctableAvailable && correctable > 0:
		status = "degraded"
	default:
		status = "healthy"
	}

	return ECCHealth{
		Enabled:                  true,
		TotalCorrectableErrors:   correctable,
		TotalUncorrectableErrors: uncorrectable,
		Status:                   status,
	}
}

// checkPerformance evaluates GPU utilization and performance state.
func (h *GPUHealthHandler) checkPerformance(
	ctx context.Context,
	device nvml.Device,
) PerformanceHealth {
	util, err := device.GetUtilizationRates(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get utilization", "error", err)
		return PerformanceHealth{
			Status: "unknown",
		}
	}

	// Get clock frequencies; report actual values or leave as 0 with a log.
	// A 0 value here means the data was unavailable, not that the clock is
	// stopped. Consumers should check the status field for "unknown".
	var smClock, memClock uint32
	if val, err := device.GetClockInfo(ctx, nvml.ClockGraphics); err != nil {
		klog.V(2).InfoS("failed to get SM clock, data unavailable", "error", err)
	} else {
		smClock = val
	}
	if val, err := device.GetClockInfo(ctx, nvml.ClockMemory); err != nil {
		klog.V(2).InfoS("failed to get memory clock, data unavailable", "error", err)
	} else {
		memClock = val
	}

	var status string
	switch {
	case util.GPU >= 95:
		status = "saturated"
	case util.GPU >= 50:
		status = "active"
	default:
		status = "idle"
	}

	return PerformanceHealth{
		GPUUtil:     util.GPU,
		MemoryUtil:  util.Memory,
		SMClock:     smClock,
		MemoryClock: memClock,
		Status:      status,
	}
}

// calculateHealthScore computes a weighted health score (0-100) by
// delegating to component-specific scorers.
func (h *GPUHealthHandler) calculateHealthScore(health *GPUHealthStatus) int {
	score := 100
	score -= h.scoreThermal(health)
	score -= h.scoreMemory(health)
	score -= h.scorePower(health)
	score -= h.scoreThrottling(health)
	score -= h.scoreECC(health)

	if score < 0 {
		score = 0
	}
	return score
}

// scoreThermal returns the penalty (0-30) for temperature issues.
func (h *GPUHealthHandler) scoreThermal(health *GPUHealthStatus) int {
	switch health.Temperature.Status {
	case "critical":
		health.Issues = append(health.Issues, HealthIssue{
			Severity:  "critical",
			Component: "temperature",
			Message: fmt.Sprintf("GPU temperature critical: %d°C",
				health.Temperature.Current),
			Suggestion: "Check cooling system, reduce workload immediately",
		})
		return 30
	case "high":
		health.Issues = append(health.Issues, HealthIssue{
			Severity:  "warning",
			Component: "temperature",
			Message: fmt.Sprintf("GPU temperature high: %d°C",
				health.Temperature.Current),
			Suggestion: "Monitor temperature, check cooling",
		})
		return 20
	case "elevated":
		return 10
	}
	return 0
}

// scoreMemory returns the penalty (0-20) for memory usage issues.
func (h *GPUHealthHandler) scoreMemory(health *GPUHealthStatus) int {
	switch health.Memory.Status {
	case "critical":
		health.Issues = append(health.Issues, HealthIssue{
			Severity:  "critical",
			Component: "memory",
			Message: fmt.Sprintf("GPU memory critically low: %.1f%% used",
				health.Memory.UsedPercent),
			Suggestion: "Free GPU memory or reduce workload",
		})
		return 20
	case "high":
		health.Issues = append(health.Issues, HealthIssue{
			Severity:  "warning",
			Component: "memory",
			Message: fmt.Sprintf("GPU memory high: %.1f%% used",
				health.Memory.UsedPercent),
			Suggestion: "Consider freeing GPU memory",
		})
		return 10
	}
	return 0
}

// scorePower returns the penalty (0-15) for power usage issues.
func (h *GPUHealthHandler) scorePower(health *GPUHealthStatus) int {
	switch health.Power.Status {
	case "over_limit":
		health.Issues = append(health.Issues, HealthIssue{
			Severity:   "warning",
			Component:  "power",
			Message:    "GPU power exceeds TDP limit",
			Suggestion: "Check power supply and cooling",
		})
		return 15
	case "high":
		return 10
	}
	return 0
}

// scoreThrottling returns the penalty (0-25) for throttling issues.
func (h *GPUHealthHandler) scoreThrottling(health *GPUHealthStatus) int {
	switch health.Throttling.Status {
	case "severe":
		health.Issues = append(health.Issues, HealthIssue{
			Severity:   "critical",
			Component:  "throttling",
			Message:    "GPU severely throttled",
			Suggestion: "Investigate thermal or power issues",
		})
		return 25
	case "minor":
		health.Issues = append(health.Issues, HealthIssue{
			Severity:   "warning",
			Component:  "throttling",
			Message:    "GPU experiencing minor throttling",
			Suggestion: "Monitor for performance impact",
		})
		return 10
	}
	return 0
}

// scoreECC returns the penalty (0-30) for ECC error issues.
func (h *GPUHealthHandler) scoreECC(health *GPUHealthStatus) int {
	if health.ECCErrors.TotalUncorrectableErrors > 0 {
		health.Issues = append(health.Issues, HealthIssue{
			Severity:  "critical",
			Component: "ecc",
			Message: fmt.Sprintf("%d uncorrectable ECC errors detected",
				health.ECCErrors.TotalUncorrectableErrors),
			Suggestion: "GPU may have hardware failure, drain node",
		})
		return 30
	}
	if health.ECCErrors.TotalCorrectableErrors > eccCorrectableThreshold {
		health.Issues = append(health.Issues, HealthIssue{
			Severity:  "warning",
			Component: "ecc",
			Message: fmt.Sprintf("%d correctable ECC errors",
				health.ECCErrors.TotalCorrectableErrors),
			Suggestion: "Monitor for increasing error rate",
		})
		return 10
	}
	return 0
}

// determineStatus classifies health based on score and issues.
func (h *GPUHealthHandler) determineStatus(
	score int,
	issues []HealthIssue,
) string {
	// Check for critical issues first
	for _, issue := range issues {
		if issue.Severity == "critical" {
			return "critical"
		}
	}

	// Score-based determination
	switch {
	case score >= 90:
		return "healthy"
	case score >= 70:
		return "warning"
	case score >= 50:
		return "degraded"
	default:
		return "critical"
	}
}

// calculateOverallHealth aggregates health across all GPUs.
func (h *GPUHealthHandler) calculateOverallHealth(
	gpus []GPUHealthStatus,
) GPUHealthResponse {
	response := GPUHealthResponse{
		DeviceCount: len(gpus),
		GPUs:        gpus,
	}

	if len(gpus) == 0 {
		response.Status = "unknown"
		response.OverallScore = 0
		return response
	}

	// Count status categories and find worst score
	worstScore := 100
	for _, gpu := range gpus {
		switch gpu.Status {
		case "healthy":
			response.HealthyCount++
		case "warning":
			response.WarningCount++
		case "degraded":
			response.DegradedCount++
		case "critical":
			response.CriticalCount++
		}

		if gpu.HealthScore < worstScore {
			worstScore = gpu.HealthScore
		}
	}

	response.OverallScore = worstScore

	// Overall status uses "worst GPU" approach
	switch {
	case response.CriticalCount > 0:
		response.Status = "critical"
	case response.DegradedCount > 0:
		response.Status = "degraded"
	case response.WarningCount > 0:
		response.Status = "warning"
	default:
		response.Status = "healthy"
	}

	return response
}

// generateRecommendation creates actionable advice based on health status.
func (h *GPUHealthHandler) generateRecommendation(
	response GPUHealthResponse,
) string {
	if response.DeviceCount == 0 {
		return "No GPU devices detected. Verify driver installation."
	}

	if response.CriticalCount > 0 {
		return fmt.Sprintf("%d GPU(s) in critical state. "+
			"Immediate investigation required. Check issues for details.",
			response.CriticalCount)
	}

	if response.DegradedCount > 0 {
		return fmt.Sprintf("%d GPU(s) degraded. "+
			"Monitor closely and investigate issues.",
			response.DegradedCount)
	}

	if response.WarningCount > 0 {
		return fmt.Sprintf("%d GPU(s) with warnings. "+
			"Review issues and monitor for changes.",
			response.WarningCount)
	}

	return "All GPUs healthy. No action required."
}

// marshalResponse marshals the response to JSON and returns as tool result.
func (h *GPUHealthHandler) marshalResponse(
	response GPUHealthResponse,
) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// GetGPUHealthTool returns the MCP tool definition for get_gpu_health.
func GetGPUHealthTool() mcp.Tool {
	return mcp.NewTool("get_gpu_health",
		mcp.WithDescription(
			"Analyze GPU operational health including temperature, "+
				"throttling, ECC errors, memory usage, and power consumption. "+
				"Returns overall health score (0-100) with status assessment "+
				"and recommendations.",
		),
	)
}
