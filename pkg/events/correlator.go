// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
)

// Default correlation configuration.
const (
	// DefaultWindowSize is the time window for event grouping around a trigger.
	DefaultWindowSize = 5 * time.Second

	// DefaultLookback is how far back to query for GPU snapshots.
	DefaultLookback = 10 * time.Minute

	// ClockSkewTolerance is extra margin for clock differences between sources.
	ClockSkewTolerance = 1 * time.Second
)

// Display and threshold constants.
const (
	// TempThresholdMargin is the °C margin below slowdown threshold for early
	// thermal warning detection.
	TempThresholdMargin = 10

	// AbsoluteHighTempThreshold is the default high temperature threshold (°C)
	// used when no GPU-specific threshold is available.
	AbsoluteHighTempThreshold = 85

	// MaxMessageLength is the character limit for event message display.
	MaxMessageLength = 80

	// ShortUUIDLength is the prefix length for abbreviated GPU UUIDs in output.
	ShortUUIDLength = 8
)

// Event represents a unified event from any source for correlation.
type Event struct {
	// Type indicates the event source: "xid", "k8s", "throttle", "ecc", "temp"
	Type string `json:"type"`

	// Timestamp when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// Source identifies the originating component.
	Source string `json:"source"` // "xid.Watcher", "K8sWatcher", "blackbox.Recorder"

	// Data contains the original event data. Expected types:
	// [xid.XIDEvent], [K8sEvent], or other source-specific event types.
	Data any `json:"data"`
}

// TimelineEntry represents a single entry in the incident timeline.
type TimelineEntry struct {
	// Timestamp is the absolute time of the event.
	Timestamp time.Time `json:"timestamp"`

	// RelativeTime is human-readable offset from trigger: "-5m", "-30s", "0s", "+10s"
	RelativeTime string `json:"relative_time"`

	// EventType categorizes the entry: "xid", "k8s", "throttle", "ecc", "temp"
	EventType string `json:"event_type"`

	// Description is a human-readable summary.
	Description string `json:"description"`

	// Severity indicates impact level: "info", "warning", "critical", "fatal"
	Severity string `json:"severity"`
}

// CorrelatedIncident groups related events from a failure scenario.
type CorrelatedIncident struct {
	// ID is a unique identifier for this incident.
	ID string `json:"id"`

	// Timestamp is when the incident was created (trigger time).
	Timestamp time.Time `json:"timestamp"`

	// Trigger is the event that initiated correlation.
	Trigger Event `json:"trigger"`

	// RelatedEvents contains all events within the correlation window.
	RelatedEvents []Event `json:"related_events"`

	// Timeline is a chronologically sorted list of all events with relative
	// timestamps. The trigger event appears at relative time "0s".
	Timeline []TimelineEntry `json:"timeline"`

	// GPUSnapshots contains GPU telemetry around the failure time.
	GPUSnapshots []blackbox.GPUSnapshot `json:"gpu_snapshots"`

	// AffectedPods lists Pods involved in the incident.
	AffectedPods []AffectedPod `json:"affected_pods"`

	// Causality is the detected failure pattern (heuristic-based).
	Causality string `json:"causality,omitempty"`
}

// AffectedPod contains Pod information from a correlated incident.
type AffectedPod struct {
	// PodName is the Pod name.
	PodName string `json:"pod_name"`

	// Namespace is the Pod's namespace.
	Namespace string `json:"namespace"`

	// PodUID is the unique identifier.
	PodUID string `json:"pod_uid,omitempty"`

	// Reason explains why this pod is affected.
	Reason string `json:"reason,omitempty"`
}

// Causality constants for detected patterns.
const (
	// CausalityThermalCascade indicates: temp elevated → throttle → XID → pod failure
	CausalityThermalCascade = "thermal_cascade"

	// CausalityMemoryFailure indicates: ECC errors → XID → pod failure
	CausalityMemoryFailure = "memory_failure"

	// CausalitySoftwareOOM indicates: OOMKilled with no hardware events
	CausalitySoftwareOOM = "software_oom"

	// CausalityUnknown when no pattern matches
	CausalityUnknown = "unknown"
)

// Correlator aggregates events from multiple sources around a trigger event.
type Correlator struct {
	recorder   *blackbox.Recorder
	k8sWatcher *K8sWatcher
	xidWatcher *xid.Watcher
	windowSize time.Duration
	lookback   time.Duration
	logger     *slog.Logger
}

// CorrelatorOption configures a Correlator.
type CorrelatorOption func(*Correlator)

// WithCorrelatorLogger sets the logger.
func WithCorrelatorLogger(logger *slog.Logger) CorrelatorOption {
	return func(c *Correlator) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithWindowSize sets the correlation time window.
// Events within ±windowSize of the trigger are included.
func WithWindowSize(d time.Duration) CorrelatorOption {
	return func(c *Correlator) {
		if d > 0 {
			c.windowSize = d
		}
	}
}

// WithLookback sets how far back to query GPU snapshots.
func WithLookback(d time.Duration) CorrelatorOption {
	return func(c *Correlator) {
		if d > 0 {
			c.lookback = d
		}
	}
}

// NewCorrelator creates a new event correlator.
// All dependencies are optional; nil dependencies are skipped during correlation.
func NewCorrelator(
	recorder *blackbox.Recorder,
	k8sWatcher *K8sWatcher,
	xidWatcher *xid.Watcher,
	opts ...CorrelatorOption,
) *Correlator {
	c := &Correlator{
		recorder:   recorder,
		k8sWatcher: k8sWatcher,
		xidWatcher: xidWatcher,
		windowSize: DefaultWindowSize,
		lookback:   DefaultLookback,
		logger:     slog.Default(),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Correlate builds a CorrelatedIncident around the trigger event.
// It queries all event sources within the configured time window.
// The context is used for cancellation; a cancelled context returns early
// with a partial incident.
func (c *Correlator) Correlate(ctx context.Context, trigger Event) *CorrelatedIncident {
	incident := &CorrelatedIncident{
		ID:        generateIncidentID(),
		Timestamp: trigger.Timestamp,
		Trigger:   trigger,
	}

	// Calculate window boundaries with clock skew tolerance
	windowStart := trigger.Timestamp.Add(-c.windowSize - ClockSkewTolerance)
	windowEnd := trigger.Timestamp.Add(c.windowSize + ClockSkewTolerance)

	// Check context before each I/O operation
	if ctx.Err() != nil {
		return incident
	}

	// 1. Gather XID events in window
	c.gatherXIDEvents(ctx, incident, windowStart, windowEnd)

	if ctx.Err() != nil {
		return incident
	}

	// 2. Gather K8s events in window
	c.gatherK8sEvents(ctx, incident, windowStart, windowEnd)

	if ctx.Err() != nil {
		return incident
	}

	// 3. Get GPU snapshots around trigger time
	c.gatherGPUSnapshots(ctx, incident, windowStart, windowEnd)

	if ctx.Err() != nil {
		return incident
	}

	// 4. Build sorted timeline
	incident.Timeline = c.buildTimeline(incident)

	// 5. Identify affected pods
	incident.AffectedPods = c.identifyAffectedPods(incident)

	// 6. Detect causality
	incident.Causality = c.DetectCausality(incident)

	c.logger.Debug("correlation complete",
		"incident_id", incident.ID,
		"related_events", len(incident.RelatedEvents),
		"timeline_entries", len(incident.Timeline),
		"affected_pods", len(incident.AffectedPods),
		"causality", incident.Causality,
	)

	return incident
}

// gatherXIDEvents collects XID events within the time window.
func (c *Correlator) gatherXIDEvents(
	ctx context.Context,
	incident *CorrelatedIncident,
	windowStart, windowEnd time.Time,
) {
	if c.xidWatcher == nil {
		return
	}

	xidEvents := c.xidWatcher.GetEvents(windowStart)
	for _, e := range xidEvents {
		if ctx.Err() != nil {
			return
		}
		if e.Timestamp.After(windowEnd) {
			continue
		}
		incident.RelatedEvents = append(incident.RelatedEvents, Event{
			Type:      "xid",
			Timestamp: e.Timestamp,
			Source:    "xid.Watcher",
			Data:      e,
		})
	}
}

// gatherK8sEvents collects Kubernetes events within the time window.
func (c *Correlator) gatherK8sEvents(
	ctx context.Context,
	incident *CorrelatedIncident,
	windowStart, windowEnd time.Time,
) {
	if c.k8sWatcher == nil {
		return
	}

	k8sEvents := c.k8sWatcher.GetEvents(windowStart)
	for _, e := range k8sEvents {
		if ctx.Err() != nil {
			return
		}
		if e.Timestamp.After(windowEnd) {
			continue
		}
		incident.RelatedEvents = append(incident.RelatedEvents, Event{
			Type:      "k8s",
			Timestamp: e.Timestamp,
			Source:    "K8sWatcher",
			Data:      e,
		})
	}
}

// gatherGPUSnapshots collects GPU telemetry within the time window.
func (c *Correlator) gatherGPUSnapshots(
	ctx context.Context,
	incident *CorrelatedIncident,
	windowStart, windowEnd time.Time,
) {
	if c.recorder == nil {
		return
	}

	snapshots := c.recorder.GetAllTimelines(c.lookback)
	for _, timeline := range snapshots {
		if ctx.Err() != nil {
			return
		}
		for _, snap := range timeline {
			if snap.Timestamp.Before(windowStart) || snap.Timestamp.After(windowEnd) {
				continue
			}
			incident.GPUSnapshots = append(incident.GPUSnapshots, snap)
		}
	}
}

// buildTimeline creates a sorted timeline of all events with relative timestamps.
func (c *Correlator) buildTimeline(incident *CorrelatedIncident) []TimelineEntry {
	var entries []TimelineEntry
	triggerTime := incident.Trigger.Timestamp

	// Add trigger event
	entries = append(entries, TimelineEntry{
		Timestamp:    triggerTime,
		RelativeTime: "0s",
		EventType:    incident.Trigger.Type,
		Description:  describeEvent(incident.Trigger),
		Severity:     severityFromEvent(incident.Trigger),
	})

	// Add related events
	for _, e := range incident.RelatedEvents {
		entries = append(entries, TimelineEntry{
			Timestamp:    e.Timestamp,
			RelativeTime: formatRelativeTime(e.Timestamp, triggerTime),
			EventType:    e.Type,
			Description:  describeEvent(e),
			Severity:     severityFromEvent(e),
		})
	}

	// Add GPU snapshot events (throttling, ECC, high temp)
	entries = append(entries, buildGPUSnapshotEntries(
		incident.GPUSnapshots, triggerTime)...)

	// Sort by timestamp
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	// Deduplicate consecutive entries with same type and description
	entries = deduplicateTimeline(entries)

	return entries
}

// buildGPUSnapshotEntries converts GPU snapshots into timeline entries for
// throttling, ECC errors, and high temperature conditions.
func buildGPUSnapshotEntries(
	snapshots []blackbox.GPUSnapshot,
	triggerTime time.Time,
) []TimelineEntry {
	var entries []TimelineEntry
	for _, snap := range snapshots {
		if snap.IsThrottled() {
			entries = append(entries, TimelineEntry{
				Timestamp:    snap.Timestamp,
				RelativeTime: formatRelativeTime(snap.Timestamp, triggerTime),
				EventType:    "throttle",
				Description:  formatThrottleDescription(snap),
				Severity:     "warning",
			})
		}
		if snap.HasECCErrors() {
			entries = append(entries, TimelineEntry{
				Timestamp:    snap.Timestamp,
				RelativeTime: formatRelativeTime(snap.Timestamp, triggerTime),
				EventType:    "ecc",
				Description:  formatECCDescription(snap),
				Severity:     severityFromECC(snap),
			})
		}
		if isHighTemperature(snap) {
			entries = append(entries, TimelineEntry{
				Timestamp:    snap.Timestamp,
				RelativeTime: formatRelativeTime(snap.Timestamp, triggerTime),
				EventType:    "temp",
				Description:  formatTempDescription(snap),
				Severity:     "warning",
			})
		}
	}
	return entries
}

// identifyAffectedPods extracts Pod information from all event sources,
// including the trigger event and all related events.
func (c *Correlator) identifyAffectedPods(incident *CorrelatedIncident) []AffectedPod {
	podMap := make(map[string]*AffectedPod) // key: namespace/name

	// Helper to add K8s event pod
	addK8sPod := func(k8sEvent K8sEvent) {
		if k8sEvent.PodName == "" {
			return
		}
		key := k8sEvent.Namespace + "/" + k8sEvent.PodName
		if _, exists := podMap[key]; !exists {
			podMap[key] = &AffectedPod{
				PodName:   k8sEvent.PodName,
				Namespace: k8sEvent.Namespace,
				PodUID:    k8sEvent.PodUID,
				Reason:    k8sEvent.Reason,
			}
		}
	}

	// Helper to add XID event pod
	addXIDPod := func(xidEvent xid.XIDEvent) {
		if xidEvent.PodName == "" {
			return
		}
		key := xidEvent.Namespace + "/" + xidEvent.PodName
		if _, exists := podMap[key]; !exists {
			podMap[key] = &AffectedPod{
				PodName:   xidEvent.PodName,
				Namespace: xidEvent.Namespace,
				Reason:    fmt.Sprintf("XID %d", xidEvent.XIDCode),
			}
		}
	}

	// From Trigger event first
	switch incident.Trigger.Type {
	case "k8s":
		if k8sEvent, ok := incident.Trigger.Data.(K8sEvent); ok {
			addK8sPod(k8sEvent)
		}
	case "xid":
		if xidEvent, ok := incident.Trigger.Data.(xid.XIDEvent); ok {
			addXIDPod(xidEvent)
		}
	}

	// From K8s related events
	for _, e := range incident.RelatedEvents {
		if e.Type != "k8s" {
			continue
		}
		if k8sEvent, ok := e.Data.(K8sEvent); ok {
			addK8sPod(k8sEvent)
		}
	}

	// From XID related events
	for _, e := range incident.RelatedEvents {
		if e.Type != "xid" {
			continue
		}
		if xidEvent, ok := e.Data.(xid.XIDEvent); ok {
			addXIDPod(xidEvent)
		}
	}

	// From GPU snapshots with process info
	for _, snap := range incident.GPUSnapshots {
		for _, proc := range snap.Processes {
			if proc.PodName == "" {
				continue
			}
			key := proc.Namespace + "/" + proc.PodName
			if _, exists := podMap[key]; !exists {
				podMap[key] = &AffectedPod{
					PodName:   proc.PodName,
					Namespace: proc.Namespace,
					PodUID:    proc.PodUID,
					Reason:    "GPU process",
				}
			}
		}
	}

	// Convert map to sorted slice for deterministic output
	pods := make([]AffectedPod, 0, len(podMap))
	for _, pod := range podMap {
		pods = append(pods, *pod)
	}
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].PodName < pods[j].PodName
	})

	return pods
}

// DetectCausality analyzes the incident timeline and returns a causality
// assessment. Returns one of [CausalityThermalCascade], [CausalityMemoryFailure],
// [CausalitySoftwareOOM], or [CausalityUnknown].
func (c *Correlator) DetectCausality(incident *CorrelatedIncident) string {
	timeline := incident.Timeline

	// Extract event type sequence
	types := make([]string, len(timeline))
	for i, e := range timeline {
		types[i] = e.EventType
	}

	// Pattern: temp_elevated → throttle → xid → k8s(pod_failed)
	// Thermal cascade: high temperature causes throttling, then XID error.
	// Requires both temp→throttle AND throttle→xid sequences for full pattern.
	if hasSequence(types, "temp", "throttle") && hasSequence(types, "throttle", "xid") {
		return CausalityThermalCascade
	}

	// Pattern: ecc → xid → k8s(pod_failed)
	// Memory failure: ECC errors indicate memory corruption leading to XID
	if hasSequence(types, "ecc", "xid") {
		return CausalityMemoryFailure
	}

	// Pattern: k8s OOMKilled without hardware events
	hasHardwareEvent := contains(types, "xid") || contains(types, "ecc") || contains(types, "throttle")
	hasOOM := c.hasOOMEvent(incident)

	if hasOOM && !hasHardwareEvent {
		return CausalitySoftwareOOM
	}

	return CausalityUnknown
}

// hasOOMEvent checks if any K8s event in the incident is an OOMKilled.
func (c *Correlator) hasOOMEvent(incident *CorrelatedIncident) bool {
	// Check trigger
	if incident.Trigger.Type == "k8s" {
		if k8sEvent, ok := incident.Trigger.Data.(K8sEvent); ok {
			if k8sEvent.Reason == ReasonOOMKilled {
				return true
			}
		}
	}

	// Check related events
	for _, e := range incident.RelatedEvents {
		if e.Type != "k8s" {
			continue
		}
		if k8sEvent, ok := e.Data.(K8sEvent); ok {
			if k8sEvent.Reason == ReasonOOMKilled {
				return true
			}
		}
	}
	return false
}

// RegisterAutoCorrelation sets up handlers for automatic correlation on
// critical events. Returns a cleanup function that stops the handlers and
// waits for any in-flight correlations to complete. The context is used
// for cancellation of correlation operations.
func (c *Correlator) RegisterAutoCorrelation(
	ctx context.Context,
	callback func(*CorrelatedIncident),
) func() {
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)

	// Register XID handler for critical/fatal events
	if c.xidWatcher != nil {
		c.xidWatcher.RegisterHandler(func(event xid.XIDEvent) {
			if ctx.Err() != nil {
				return
			}
			if event.Severity == "critical" || event.Severity == "fatal" {
				wg.Add(1)
				go func() {
					defer wg.Done()
					trigger := Event{
						Type:      "xid",
						Timestamp: event.Timestamp,
						Source:    "xid.Watcher",
						Data:      event,
					}
					incident := c.Correlate(ctx, trigger)
					if ctx.Err() == nil {
						callback(incident)
					}
				}()
			}
		})
	}

	// Register K8s handler for Warning events (OOMKilled, Failed, etc.)
	if c.k8sWatcher != nil {
		c.k8sWatcher.RegisterHandler(func(event K8sEvent) {
			if ctx.Err() != nil {
				return
			}
			if event.Type == "Warning" && (event.Reason == ReasonOOMKilled || event.Reason == ReasonFailed) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					trigger := Event{
						Type:      "k8s",
						Timestamp: event.Timestamp,
						Source:    "K8sWatcher",
						Data:      event,
					}
					incident := c.Correlate(ctx, trigger)
					if ctx.Err() == nil {
						callback(incident)
					}
				}()
			}
		})
	}

	// Return cleanup function
	return func() {
		cancel()
		wg.Wait()
	}
}

// --- Helper functions ---

// generateIncidentID creates a unique identifier for an incident.
func generateIncidentID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Log and fallback to timestamp-based ID on error
		slog.Warn("crypto/rand unavailable, using timestamp-based incident ID",
			"error", err)
		return fmt.Sprintf("inc-%d", time.Now().UnixNano())
	}
	return "inc-" + hex.EncodeToString(b)
}

// formatRelativeTime returns a human-readable relative timestamp.
func formatRelativeTime(t, reference time.Time) string {
	d := t.Sub(reference)

	// Handle exact zero
	if d == 0 {
		return "0s"
	}

	// Format sign and magnitude
	sign := "+"
	if d < 0 {
		sign = "-"
		d = -d
	}

	// Choose appropriate unit
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%s%dh", sign, int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%s%dm", sign, int(d.Minutes()))
	case d >= time.Second:
		return fmt.Sprintf("%s%ds", sign, int(d.Seconds()))
	default:
		return fmt.Sprintf("%s%dms", sign, d.Milliseconds())
	}
}

// describeEvent returns a human-readable description of an event.
func describeEvent(e Event) string {
	switch e.Type {
	case "xid":
		if xidEvent, ok := e.Data.(xid.XIDEvent); ok {
			if xidEvent.Description != "" {
				return fmt.Sprintf("XID %d: %s", xidEvent.XIDCode, xidEvent.Description)
			}
			return fmt.Sprintf("XID %d on GPU %s", xidEvent.XIDCode, xidEvent.PCIBusID)
		}
	case "k8s":
		if k8sEvent, ok := e.Data.(K8sEvent); ok {
			return fmt.Sprintf("[%s] %s: %s", k8sEvent.Reason, k8sEvent.PodName, truncate(k8sEvent.Message, MaxMessageLength))
		}
	case "throttle":
		return "GPU throttling active"
	case "ecc":
		return "ECC memory errors detected"
	case "temp":
		return "High GPU temperature"
	}
	return fmt.Sprintf("%s event", e.Type)
}

// severityFromEvent determines severity based on event type and data.
func severityFromEvent(e Event) string {
	switch e.Type {
	case "xid":
		if xidEvent, ok := e.Data.(xid.XIDEvent); ok {
			if xidEvent.Severity != "" {
				return xidEvent.Severity
			}
		}
		return "critical"
	case "k8s":
		if k8sEvent, ok := e.Data.(K8sEvent); ok {
			switch k8sEvent.Reason {
			case ReasonOOMKilled, ReasonFailed:
				return "critical"
			case ReasonEvicted, ReasonKilling:
				return "warning"
			}
		}
		return "warning"
	case "throttle":
		return "warning"
	case "ecc":
		return "critical"
	case "temp":
		return "warning"
	}
	return "info"
}

// severityFromECC determines severity based on ECC error counts.
func severityFromECC(snap blackbox.GPUSnapshot) string {
	if snap.ECCUncorrectable > 0 {
		return "critical"
	}
	return "warning"
}

// formatThrottleDescription formats a throttling event description.
func formatThrottleDescription(snap blackbox.GPUSnapshot) string {
	uuid := snap.UUID
	if len(uuid) > ShortUUIDLength {
		uuid = uuid[:ShortUUIDLength]
	}
	return fmt.Sprintf("GPU %s throttling active (reason: 0x%x)", uuid, snap.Throttling)
}

// formatECCDescription formats an ECC error description.
func formatECCDescription(snap blackbox.GPUSnapshot) string {
	uuid := snap.UUID
	if len(uuid) > ShortUUIDLength {
		uuid = uuid[:ShortUUIDLength]
	}
	return fmt.Sprintf("GPU %s ECC errors: %d correctable, %d uncorrectable",
		uuid, snap.ECCCorrectable, snap.ECCUncorrectable)
}

// formatTempDescription formats a high temperature description.
func formatTempDescription(snap blackbox.GPUSnapshot) string {
	uuid := snap.UUID
	if len(uuid) > ShortUUIDLength {
		uuid = uuid[:ShortUUIDLength]
	}
	return fmt.Sprintf("GPU %s temperature: %d°C (threshold: %d°C)",
		uuid, snap.Temperature, snap.TempThreshold)
}

// isHighTemperature checks if temperature is approaching threshold.
// Returns true if temperature is within TempThresholdMargin of slowdown threshold.
func isHighTemperature(snap blackbox.GPUSnapshot) bool {
	if snap.TempThreshold == 0 {
		// No threshold available, use absolute limit
		return snap.Temperature >= AbsoluteHighTempThreshold
	}
	// Within margin of threshold
	return snap.Temperature >= snap.TempThreshold-TempThresholdMargin
}

// hasSequence checks if events of type a appear before type b in the slice.
func hasSequence(types []string, a, b string) bool {
	foundA := -1
	for i, t := range types {
		if t == a && foundA < 0 {
			foundA = i
		}
		if t == b && foundA >= 0 {
			return true
		}
	}
	return false
}

// contains checks if the slice contains the given element.
func contains(types []string, target string) bool {
	for _, t := range types {
		if t == target {
			return true
		}
	}
	return false
}

// truncate shortens a string to maxLen, adding ellipsis if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// deduplicateTimeline removes all entries with duplicate type+description,
// keeping only the first occurrence of each unique combination.
func deduplicateTimeline(entries []TimelineEntry) []TimelineEntry {
	if len(entries) <= 1 {
		return entries
	}

	seen := make(map[string]bool)
	result := make([]TimelineEntry, 0, len(entries))

	for _, entry := range entries {
		key := entry.EventType + "|" + entry.Description
		if !seen[key] {
			seen[key] = true
			result = append(result, entry)
		}
	}

	return result
}
