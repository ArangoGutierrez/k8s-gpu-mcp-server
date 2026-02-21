// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
)

// ExplanationData provides template context derived from CorrelatedIncident.
type ExplanationData struct {
	// Incident identification
	IncidentID string
	Timestamp  time.Time

	// Causality
	Causality      string // raw: "thermal_cascade", "memory_failure", etc.
	CausalityHuman string // human: "hardware thermal issue", etc.

	// Pod info
	PodName   string
	Namespace string

	// Node info
	Node string

	// Hardware state at failure
	Temperature      uint32
	TempThreshold    uint32
	MemUsed          uint64
	MemTotal         uint64
	MemPercent       float64
	ECCCorrectable   uint64
	ECCUncorrectable uint64
	Throttling       uint64
	ThrottleDuration string

	// XID info
	XIDCode        int
	XIDName        string
	XIDDescription string
	XIDAction      string
	XIDCategory    string

	// Timeline
	Timeline []events.TimelineEntry

	// Derived
	NotYourCode bool
}

// Explainer generates human-readable explanations from incident data.
type Explainer struct {
	explanationTemplates map[string]*template.Template
	summaryTemplates     map[string]*template.Template
}

// NewExplainer creates a new Explainer with compiled templates.
// Returns an error if any template fails to parse.
func NewExplainer() (*Explainer, error) {
	e := &Explainer{
		explanationTemplates: make(map[string]*template.Template),
		summaryTemplates:     make(map[string]*template.Template),
	}

	// Parse explanation templates
	for name, tmplStr := range explanationTemplates {
		tmpl, err := template.New(name).Funcs(templateFuncs).Parse(tmplStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse explanation template %q: %w", name, err)
		}
		e.explanationTemplates[name] = tmpl
	}

	// Parse summary templates
	for name, tmplStr := range summaryTemplates {
		tmpl, err := template.New(name + "_summary").Funcs(templateFuncs).Parse(tmplStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse summary template %q: %w", name, err)
		}
		e.summaryTemplates[name] = tmpl
	}

	return e, nil
}

// GenerateExplanation produces a full human-readable explanation of the incident.
func (e *Explainer) GenerateExplanation(incident *events.CorrelatedIncident) (result string) {
	if incident == nil {
		return "No incident data available."
	}

	data := e.extractData(incident)
	templateKey := e.selectTemplateKey(data.Causality)

	tmpl, ok := e.explanationTemplates[templateKey]
	if !ok {
		tmpl = e.explanationTemplates["unknown"]
	}

	// Recover from panics in template execution caused by malformed data
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprintf("Error generating explanation: template panic: %v", r)
		}
	}()

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("Error generating explanation: %v", err)
	}

	return strings.TrimSpace(buf.String())
}

// GenerateSummary produces a one-line summary of the incident.
func (e *Explainer) GenerateSummary(incident *events.CorrelatedIncident) (result string) {
	if incident == nil {
		return "No incident data available."
	}

	data := e.extractData(incident)
	templateKey := e.selectTemplateKey(data.Causality)

	tmpl, ok := e.summaryTemplates[templateKey]
	if !ok {
		tmpl = e.summaryTemplates["unknown"]
	}

	// Recover from panics in template execution caused by malformed data
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprintf("GPU failure on %s", data.Node)
		}
	}()

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("GPU failure on %s", data.Node)
	}

	return strings.TrimSpace(buf.String())
}

// GenerateTimeline produces a formatted timeline string.
func (e *Explainer) GenerateTimeline(incident *events.CorrelatedIncident) string {
	if incident == nil || len(incident.Timeline) == 0 {
		return "No timeline data available."
	}

	var buf bytes.Buffer
	buf.WriteString("## Timeline\n\n")

	for _, entry := range incident.Timeline {
		fmt.Fprintf(&buf, "- %s (%s) - %s\n",
			entry.Timestamp.Format("15:04:05"),
			entry.RelativeTime,
			entry.Description,
		)
	}

	return buf.String()
}

// extractData populates ExplanationData from a CorrelatedIncident.
func (e *Explainer) extractData(incident *events.CorrelatedIncident) ExplanationData {
	data := ExplanationData{
		IncidentID: incident.ID,
		Timestamp:  incident.Timestamp,
		Causality:  incident.Causality,
		Timeline:   incident.Timeline,
	}

	// Map causality to human-readable and NotYourCode flag
	switch incident.Causality {
	case events.CausalityThermalCascade:
		data.CausalityHuman = "hardware thermal issue"
		data.NotYourCode = true
	case events.CausalityMemoryFailure:
		data.CausalityHuman = "hardware memory failure"
		data.NotYourCode = true
	case events.CausalitySoftwareOOM:
		data.CausalityHuman = "GPU memory exhaustion"
		data.NotYourCode = false
	default:
		data.CausalityHuman = "unknown failure"
		data.NotYourCode = false
	}

	// Extract Pod info (first affected pod)
	if len(incident.AffectedPods) > 0 {
		pod := incident.AffectedPods[0]
		data.PodName = pod.PodName
		data.Namespace = pod.Namespace
	}

	// Extract hardware state from GPUSnapshots
	if len(incident.GPUSnapshots) > 0 {
		// Use the snapshot closest to the trigger time
		snap := e.findClosestSnapshot(incident.GPUSnapshots, incident.Timestamp)
		data.Temperature = snap.Temperature
		data.TempThreshold = snap.TempThreshold
		data.MemUsed = snap.MemUsed
		data.MemTotal = snap.MemTotal
		data.MemPercent = snap.MemoryUsagePercent()
		data.ECCCorrectable = snap.ECCCorrectable
		data.ECCUncorrectable = snap.ECCUncorrectable
		data.Throttling = snap.Throttling

		// Calculate throttle duration if throttling occurred
		data.ThrottleDuration = e.calculateThrottleDuration(incident.GPUSnapshots)
	}

	// Extract XID info from trigger or related events
	e.extractXIDInfo(&data, incident)

	// Extract Node info
	data.Node = e.extractNode(incident)

	return data
}

// selectTemplateKey maps causality to template key.
func (e *Explainer) selectTemplateKey(causality string) string {
	switch causality {
	case events.CausalityThermalCascade:
		return "hardware_thermal"
	case events.CausalityMemoryFailure:
		return "hardware_memory"
	case events.CausalitySoftwareOOM:
		return "software_oom"
	default:
		return "unknown"
	}
}

// findClosestSnapshot returns the snapshot closest to the target time.
func (e *Explainer) findClosestSnapshot(snapshots []blackbox.GPUSnapshot, target time.Time) blackbox.GPUSnapshot {
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

// calculateThrottleDuration calculates how long throttling was active.
func (e *Explainer) calculateThrottleDuration(snapshots []blackbox.GPUSnapshot) string {
	var firstThrottle, lastThrottle time.Time

	for _, snap := range snapshots {
		if snap.IsThrottled() {
			if firstThrottle.IsZero() {
				firstThrottle = snap.Timestamp
			}
			lastThrottle = snap.Timestamp
		}
	}

	if firstThrottle.IsZero() {
		return ""
	}

	duration := lastThrottle.Sub(firstThrottle)
	if duration < time.Second {
		return ""
	}

	return formatDuration(duration)
}

// extractXIDInfo populates XID fields from incident events.
func (e *Explainer) extractXIDInfo(data *ExplanationData, incident *events.CorrelatedIncident) {
	// Check trigger first
	if incident.Trigger.Type == "xid" {
		if xidEvent, ok := incident.Trigger.Data.(xid.XIDEvent); ok {
			e.populateXIDFromEvent(data, xidEvent)
			return
		}
	}

	// Check related events
	for _, event := range incident.RelatedEvents {
		if event.Type == "xid" {
			if xidEvent, ok := event.Data.(xid.XIDEvent); ok {
				e.populateXIDFromEvent(data, xidEvent)
				return
			}
		}
	}
}

// populateXIDFromEvent fills XID fields from an XIDEvent.
func (e *Explainer) populateXIDFromEvent(data *ExplanationData, xidEvent xid.XIDEvent) {
	data.XIDCode = xidEvent.XIDCode
	data.XIDDescription = xidEvent.Description

	// Look up additional info from codes table
	if info, ok := xid.Lookup(xidEvent.XIDCode); ok {
		data.XIDName = info.Name
		data.XIDAction = info.Action
		data.XIDCategory = info.Category
	}
}

// extractNode determines the node name from incident data.
func (e *Explainer) extractNode(incident *events.CorrelatedIncident) string {
	// Try trigger first
	if incident.Trigger.Type == "k8s" {
		if k8sEvent, ok := incident.Trigger.Data.(events.K8sEvent); ok {
			if k8sEvent.NodeName != "" {
				return k8sEvent.NodeName
			}
		}
	}

	// Try related K8s events
	for _, event := range incident.RelatedEvents {
		if event.Type == "k8s" {
			if k8sEvent, ok := event.Data.(events.K8sEvent); ok {
				if k8sEvent.NodeName != "" {
					return k8sEvent.NodeName
				}
			}
		}
	}

	// Fallback to generic
	return "the GPU node"
}

// absDuration returns the absolute value of a duration.
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
