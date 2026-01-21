// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
)

// Analyzer performs root cause analysis on correlated incidents using
// pattern matching with confidence scoring.
type Analyzer struct {
	patterns []FailurePattern
}

// NewAnalyzer creates an Analyzer with the default known patterns.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		patterns: KnownPatterns,
	}
}

// NewAnalyzerWithPatterns creates an Analyzer with custom patterns.
// Useful for testing or extending with additional patterns.
func NewAnalyzerWithPatterns(patterns []FailurePattern) *Analyzer {
	return &Analyzer{
		patterns: patterns,
	}
}

// Analyze performs pattern matching on a CorrelatedIncident and returns an
// IncidentReport with confidence-scored root cause and recommendations.
func (a *Analyzer) Analyze(incident *events.CorrelatedIncident) *IncidentReport {
	if incident == nil {
		return a.emptyReport()
	}

	// Extract data from incident
	ctx := a.extractContext(incident)

	// Match patterns and find highest confidence
	bestPattern, confidence, evidence := a.matchPatterns(ctx)

	// Build report
	report := &IncidentReport{
		ID:        incident.ID,
		Timestamp: incident.Timestamp,
		Timeline:  incident.Timeline,
		RootCause: RootCause{
			Category:    bestPattern.Category,
			Confidence:  confidence,
			Evidence:    evidence,
			NotYourCode: bestPattern.RootCause.NotYourCode,
		},
	}

	// Populate pod info
	if len(incident.AffectedPods) > 0 {
		pod := incident.AffectedPods[0]
		report.Pod = PodInfo{
			Name:      pod.PodName,
			Namespace: pod.Namespace,
			UID:       pod.PodUID,
		}
	}

	// Populate node
	report.Node = a.extractNode(incident)

	// Populate GPU UUID
	report.GPUUUID = a.extractGPUUUID(incident)

	// Populate hardware state
	report.HardwareState = a.extractHardwareState(incident)

	// Template recommendations
	report.Recommendations = a.templateRecommendations(bestPattern.Recommendations, report)

	return report
}

// emptyReport returns a minimal report for nil input.
func (a *Analyzer) emptyReport() *IncidentReport {
	return &IncidentReport{
		ID:        "unknown",
		Timestamp: time.Now(),
		RootCause: RootCause{
			Category:    CategoryUnknown,
			Confidence:  0.0,
			Evidence:    []string{"No incident data provided"},
			NotYourCode: false,
		},
		Recommendations: []Recommendation{
			{
				Action:   "Collect more diagnostic data",
				Priority: PriorityMedium,
			},
		},
	}
}

// analysisContext holds extracted data from incident for pattern matching.
type analysisContext struct {
	// XID codes found in incident
	xidCodes []int

	// Temperature (highest observed)
	temperature uint32

	// ECC uncorrectable count
	eccUncorrectable uint64

	// Memory utilization percentage
	memUtilPercent float64

	// Throttle reasons active
	throttleReasons uint64

	// K8s event reasons and messages
	k8sReasons  []string
	k8sMessages []string

	// Causality from correlator
	causality string
}

// extractContext gathers relevant data from incident for pattern matching.
func (a *Analyzer) extractContext(incident *events.CorrelatedIncident) *analysisContext {
	ctx := &analysisContext{
		causality: incident.Causality,
	}

	// Extract XID codes
	ctx.xidCodes = a.extractXIDCodes(incident)

	// Extract hardware metrics from GPU snapshots
	if len(incident.GPUSnapshots) > 0 {
		// Find the snapshot with worst conditions
		for _, snap := range incident.GPUSnapshots {
			if snap.Temperature > ctx.temperature {
				ctx.temperature = snap.Temperature
			}
			if snap.ECCUncorrectable > ctx.eccUncorrectable {
				ctx.eccUncorrectable = snap.ECCUncorrectable
			}
			if snap.Throttling != 0 {
				ctx.throttleReasons |= snap.Throttling
			}
			memPct := snap.MemoryUsagePercent()
			if memPct > ctx.memUtilPercent {
				ctx.memUtilPercent = memPct
			}
		}
	}

	// Extract K8s event data
	a.extractK8sEvents(incident, ctx)

	return ctx
}

// extractXIDCodes collects all XID codes from incident events.
func (a *Analyzer) extractXIDCodes(incident *events.CorrelatedIncident) []int {
	var codes []int
	seen := make(map[int]bool)

	// Check trigger
	if incident.Trigger.Type == "xid" {
		if xidEvent, ok := incident.Trigger.Data.(xid.XIDEvent); ok {
			if !seen[xidEvent.XIDCode] {
				codes = append(codes, xidEvent.XIDCode)
				seen[xidEvent.XIDCode] = true
			}
		}
	}

	// Check related events
	for _, e := range incident.RelatedEvents {
		if e.Type == "xid" {
			if xidEvent, ok := e.Data.(xid.XIDEvent); ok {
				if !seen[xidEvent.XIDCode] {
					codes = append(codes, xidEvent.XIDCode)
					seen[xidEvent.XIDCode] = true
				}
			}
		}
	}

	return codes
}

// extractK8sEvents gathers K8s event reasons and messages.
func (a *Analyzer) extractK8sEvents(incident *events.CorrelatedIncident, ctx *analysisContext) {
	// Check trigger
	if incident.Trigger.Type == "k8s" {
		if k8sEvent, ok := incident.Trigger.Data.(events.K8sEvent); ok {
			ctx.k8sReasons = append(ctx.k8sReasons, k8sEvent.Reason)
			ctx.k8sMessages = append(ctx.k8sMessages, k8sEvent.Message)
		}
	}

	// Check related events
	for _, e := range incident.RelatedEvents {
		if e.Type == "k8s" {
			if k8sEvent, ok := e.Data.(events.K8sEvent); ok {
				ctx.k8sReasons = append(ctx.k8sReasons, k8sEvent.Reason)
				ctx.k8sMessages = append(ctx.k8sMessages, k8sEvent.Message)
			}
		}
	}
}

// matchPatterns evaluates all patterns and returns the best match.
func (a *Analyzer) matchPatterns(ctx *analysisContext) (FailurePattern, float64, []string) {
	var bestPattern FailurePattern
	var bestConfidence float64
	var bestEvidence []string

	// Default to unknown pattern
	unknownPattern := FailurePattern{
		Name:     "unknown",
		Category: CategoryUnknown,
		RootCause: PatternRootCause{
			Category:    CategoryUnknown,
			NotYourCode: false,
		},
		Recommendations: []Recommendation{
			{
				Action:   "Review incident timeline and logs for additional context",
				Priority: PriorityMedium,
			},
			{
				Action:   "Check GPU health metrics",
				Command:  "nvidia-smi -q",
				Priority: PriorityMedium,
			},
		},
	}
	bestPattern = unknownPattern

	for _, pattern := range a.patterns {
		confidence, evidence := a.evaluatePattern(pattern, ctx)
		if confidence > bestConfidence {
			bestConfidence = confidence
			bestPattern = pattern
			bestEvidence = evidence
		}
	}

	// If no pattern matched well, use unknown
	// Require at least 0.2 confidence (more than one weak indicator)
	if bestConfidence < 0.2 {
		return unknownPattern, 0.0, []string{"No known failure pattern matched"}
	}

	return bestPattern, bestConfidence, bestEvidence
}

// evaluatePattern checks a single pattern against the context.
// Returns confidence score and evidence strings.
func (a *Analyzer) evaluatePattern(pattern FailurePattern, ctx *analysisContext) (float64, []string) {
	var confidence float64
	var evidence []string

	for _, indicator := range pattern.Indicators {
		matched, evStr := a.checkIndicator(indicator, ctx)
		if matched {
			confidence += indicator.Weight
			if evStr != "" {
				evidence = append(evidence, evStr)
			}
		}
	}

	// Cap confidence at 1.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence, evidence
}

// checkIndicator evaluates a single indicator against the context.
func (a *Analyzer) checkIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	switch ind.Type {
	case IndicatorTypeXID:
		return a.checkXIDIndicator(ind, ctx)
	case IndicatorTypeTemp:
		return a.checkTempIndicator(ind, ctx)
	case IndicatorTypeECC:
		return a.checkECCIndicator(ind, ctx)
	case IndicatorTypeThrottle:
		return a.checkThrottleIndicator(ind, ctx)
	case IndicatorTypeK8sEvent:
		return a.checkK8sEventIndicator(ind, ctx)
	case IndicatorTypeMemUtil:
		return a.checkMemUtilIndicator(ind, ctx)
	case IndicatorTypeCausality:
		return a.checkCausalityIndicator(ind, ctx)
	default:
		return false, ""
	}
}

// checkXIDIndicator evaluates XID-related conditions.
func (a *Analyzer) checkXIDIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	switch ind.Condition {
	case ConditionEquals:
		targetCode, ok := toInt(ind.Value)
		if !ok {
			return false, ""
		}
		for _, code := range ctx.xidCodes {
			if code == targetCode {
				return true, fmt.Sprintf("XID %d detected", code)
			}
		}
	case ConditionIn:
		targetCodes, ok := ind.Value.([]int)
		if !ok {
			return false, ""
		}
		for _, code := range ctx.xidCodes {
			for _, target := range targetCodes {
				if code == target {
					return true, fmt.Sprintf("XID %d detected (in critical set)", code)
				}
			}
		}
	case ConditionNotPresent:
		if len(ctx.xidCodes) == 0 {
			return true, "No hardware XID errors"
		}
	case ConditionPresent:
		if len(ctx.xidCodes) > 0 {
			return true, fmt.Sprintf("XID errors present: %v", ctx.xidCodes)
		}
	}
	return false, ""
}

// checkTempIndicator evaluates temperature conditions.
func (a *Analyzer) checkTempIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	switch ind.Condition {
	case ConditionGreaterThan:
		threshold, ok := toUint32(ind.Value)
		if !ok {
			return false, ""
		}
		if ctx.temperature > threshold {
			return true, fmt.Sprintf("GPU temperature %d°C exceeds threshold %d°C", ctx.temperature, threshold)
		}
	case ConditionLessThan:
		threshold, ok := toUint32(ind.Value)
		if !ok {
			return false, ""
		}
		if ctx.temperature < threshold {
			return true, fmt.Sprintf("GPU temperature %d°C below %d°C", ctx.temperature, threshold)
		}
	}
	return false, ""
}

// checkECCIndicator evaluates ECC error conditions.
func (a *Analyzer) checkECCIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	switch ind.Condition {
	case ConditionGreaterThan:
		threshold, ok := toUint64(ind.Value)
		if !ok {
			return false, ""
		}
		if ctx.eccUncorrectable > threshold {
			return true, fmt.Sprintf("Uncorrectable ECC errors: %d", ctx.eccUncorrectable)
		}
	case ConditionEquals:
		target, ok := toUint64(ind.Value)
		if !ok {
			return false, ""
		}
		if ctx.eccUncorrectable == target {
			return true, fmt.Sprintf("ECC error count: %d", ctx.eccUncorrectable)
		}
	}
	return false, ""
}

// checkThrottleIndicator evaluates throttling conditions.
func (a *Analyzer) checkThrottleIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	switch ind.Condition {
	case ConditionPresent:
		if ctx.throttleReasons != 0 {
			// Check for specific throttle type if value provided
			if ind.Value != nil {
				if valStr, ok := ind.Value.(string); ok && valStr != "" {
					if strings.Contains(valStr, "thermal") && ctx.throttleReasons != 0 {
						return true, fmt.Sprintf("Thermal throttling active (reason: 0x%x)", ctx.throttleReasons)
					}
				}
			}
			return true, fmt.Sprintf("GPU throttling active (reason: 0x%x)", ctx.throttleReasons)
		}
	case ConditionNotPresent:
		if ctx.throttleReasons == 0 {
			return true, "No throttling detected"
		}
	}
	return false, ""
}

// checkK8sEventIndicator evaluates Kubernetes event conditions.
func (a *Analyzer) checkK8sEventIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	searchStr, ok := ind.Value.(string)
	if !ok {
		return false, ""
	}

	switch ind.Condition {
	case ConditionContains:
		// Check reasons
		for _, reason := range ctx.k8sReasons {
			if strings.Contains(strings.ToLower(reason), strings.ToLower(searchStr)) {
				return true, fmt.Sprintf("K8s event reason: %s", reason)
			}
		}
		// Check messages
		for _, msg := range ctx.k8sMessages {
			if strings.Contains(strings.ToLower(msg), strings.ToLower(searchStr)) {
				return true, fmt.Sprintf("K8s event contains: %s", searchStr)
			}
		}
	case ConditionEquals:
		for _, reason := range ctx.k8sReasons {
			if strings.EqualFold(reason, searchStr) {
				return true, fmt.Sprintf("K8s event reason: %s", reason)
			}
		}
	}
	return false, ""
}

// checkMemUtilIndicator evaluates memory utilization conditions.
func (a *Analyzer) checkMemUtilIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	switch ind.Condition {
	case ConditionGreaterThan:
		threshold, ok := toFloat64(ind.Value)
		if !ok {
			return false, ""
		}
		if ctx.memUtilPercent > threshold {
			return true, fmt.Sprintf("GPU memory utilization %.1f%% exceeds %.1f%%", ctx.memUtilPercent, threshold)
		}
	case ConditionLessThan:
		threshold, ok := toFloat64(ind.Value)
		if !ok {
			return false, ""
		}
		if ctx.memUtilPercent < threshold {
			return true, fmt.Sprintf("GPU memory utilization %.1f%% below %.1f%%", ctx.memUtilPercent, threshold)
		}
	}
	return false, ""
}

// checkCausalityIndicator evaluates correlator's causality assessment.
func (a *Analyzer) checkCausalityIndicator(ind Indicator, ctx *analysisContext) (bool, string) {
	switch ind.Condition {
	case ConditionEquals:
		targetCausality, ok := ind.Value.(string)
		if !ok {
			return false, ""
		}
		if ctx.causality == targetCausality {
			return true, fmt.Sprintf("Correlator detected: %s", ctx.causality)
		}
	case ConditionContains:
		targetCausality, ok := ind.Value.(string)
		if !ok {
			return false, ""
		}
		if strings.Contains(ctx.causality, targetCausality) {
			return true, fmt.Sprintf("Correlator causality includes: %s", targetCausality)
		}
	}
	return false, ""
}

// extractNode determines the node name from incident data.
func (a *Analyzer) extractNode(incident *events.CorrelatedIncident) string {
	// Try trigger first
	if incident.Trigger.Type == "k8s" {
		if k8sEvent, ok := incident.Trigger.Data.(events.K8sEvent); ok {
			if k8sEvent.NodeName != "" {
				return k8sEvent.NodeName
			}
		}
	}

	// Try related K8s events
	for _, e := range incident.RelatedEvents {
		if e.Type == "k8s" {
			if k8sEvent, ok := e.Data.(events.K8sEvent); ok {
				if k8sEvent.NodeName != "" {
					return k8sEvent.NodeName
				}
			}
		}
	}

	return ""
}

// extractGPUUUID extracts GPU UUID from incident data.
func (a *Analyzer) extractGPUUUID(incident *events.CorrelatedIncident) string {
	// Try trigger XID event first (most specific)
	if incident.Trigger.Type == "xid" {
		if xidEvent, ok := incident.Trigger.Data.(xid.XIDEvent); ok {
			if xidEvent.GPUUUID != "" {
				return xidEvent.GPUUUID
			}
		}
	}

	// Try GPU snapshots
	if len(incident.GPUSnapshots) > 0 && incident.GPUSnapshots[0].UUID != "" {
		return incident.GPUSnapshots[0].UUID
	}

	// Try related XID events
	for _, e := range incident.RelatedEvents {
		if e.Type == "xid" {
			if xidEvent, ok := e.Data.(xid.XIDEvent); ok {
				if xidEvent.GPUUUID != "" {
					return xidEvent.GPUUUID
				}
			}
		}
	}

	return ""
}

// extractHardwareState builds HardwareSnapshot from GPU snapshots.
func (a *Analyzer) extractHardwareState(incident *events.CorrelatedIncident) *HardwareSnapshot {
	if len(incident.GPUSnapshots) == 0 {
		return nil
	}

	// Find the snapshot closest to trigger time
	snap := a.findClosestSnapshot(incident.GPUSnapshots, incident.Timestamp)

	return &HardwareSnapshot{
		Temperature:      snap.Temperature,
		TempThreshold:    snap.TempThreshold,
		MemUsed:          snap.MemUsed,
		MemTotal:         snap.MemTotal,
		ECCCorrectable:   snap.ECCCorrectable,
		ECCUncorrectable: snap.ECCUncorrectable,
		Throttling:       snap.Throttling,
	}
}

// findClosestSnapshot returns the snapshot closest to target time.
func (a *Analyzer) findClosestSnapshot(snapshots []blackbox.GPUSnapshot, target time.Time) blackbox.GPUSnapshot {
	if len(snapshots) == 0 {
		return blackbox.GPUSnapshot{}
	}

	closest := snapshots[0]
	closestDiff := absDuration(snapshots[0].Timestamp.Sub(target))

	for _, snap := range snapshots[1:] {
		diff := absDuration(snap.Timestamp.Sub(target))
		if diff < closestDiff {
			closest = snap
			closestDiff = diff
		}
	}

	return closest
}

// templateRecommendations interpolates recommendation commands with report data.
func (a *Analyzer) templateRecommendations(recs []Recommendation, report *IncidentReport) []Recommendation {
	result := make([]Recommendation, 0, len(recs))

	data := struct {
		Node      string
		PodName   string
		Namespace string
		GPUUUID   string
	}{
		Node:      report.Node,
		PodName:   report.Pod.Name,
		Namespace: report.Pod.Namespace,
		GPUUUID:   report.GPUUUID,
	}

	// Use "the-node" as fallback if node unknown
	if data.Node == "" {
		data.Node = "<node>"
	}
	if data.PodName == "" {
		data.PodName = "<pod-name>"
	}
	if data.Namespace == "" {
		data.Namespace = "default"
	}

	for _, rec := range recs {
		newRec := Recommendation{
			Action:   rec.Action,
			Priority: rec.Priority,
		}

		if rec.Command != "" {
			tmpl, err := template.New("cmd").Parse(rec.Command)
			if err != nil {
				slog.Warn("failed to parse recommendation template",
					"command", rec.Command, "error", err)
				newRec.Command = rec.Command
			} else {
				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, data); err != nil {
					slog.Warn("failed to execute recommendation template",
						"command", rec.Command, "error", err)
					newRec.Command = rec.Command
				} else {
					newRec.Command = buf.String()
				}
			}
		}

		result = append(result, newRec)
	}

	return result
}

// Type conversion helpers

func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int32:
		return int(val), true
	case int64:
		return int(val), true
	case uint:
		return int(val), true
	case uint32:
		return int(val), true
	case uint64:
		return int(val), true
	default:
		return 0, false
	}
}

func toUint32(v any) (uint32, bool) {
	switch val := v.(type) {
	case uint32:
		return val, true
	case int:
		if val < 0 {
			return 0, false
		}
		return uint32(val), true
	case int32:
		if val < 0 {
			return 0, false
		}
		return uint32(val), true
	case int64:
		if val < 0 {
			return 0, false
		}
		return uint32(val), true
	case uint:
		return uint32(val), true
	case uint64:
		return uint32(val), true
	default:
		return 0, false
	}
}

func toUint64(v any) (uint64, bool) {
	switch val := v.(type) {
	case uint64:
		return val, true
	case int:
		if val < 0 {
			return 0, false
		}
		return uint64(val), true
	case int32:
		if val < 0 {
			return 0, false
		}
		return uint64(val), true
	case int64:
		if val < 0 {
			return 0, false
		}
		return uint64(val), true
	case uint:
		return uint64(val), true
	case uint32:
		return uint64(val), true
	default:
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	default:
		return 0, false
	}
}
