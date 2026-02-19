// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/incidents"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// GetIncidentReportHandler handles the get_incident_report tool.
type GetIncidentReportHandler struct {
	k8sClientset kubernetes.Interface
	recorder     *blackbox.Recorder
	correlator   *events.Correlator
	analyzer     *incidents.Analyzer
	explainer    *incidents.Explainer
	namespace    string
	// incidentCache stores recent incidents by ID for lookup
	incidentCache map[string]*events.CorrelatedIncident
	cacheMu       sync.RWMutex
}

// GetIncidentReportOption configures GetIncidentReportHandler.
type GetIncidentReportOption func(*GetIncidentReportHandler)

// WithIncidentRecorder sets the flight recorder.
func WithIncidentRecorder(r *blackbox.Recorder) GetIncidentReportOption {
	return func(h *GetIncidentReportHandler) {
		h.recorder = r
	}
}

// WithIncidentCorrelator sets the event correlator.
func WithIncidentCorrelator(c *events.Correlator) GetIncidentReportOption {
	return func(h *GetIncidentReportHandler) {
		h.correlator = c
	}
}

// WithIncidentAnalyzer sets the incident analyzer.
func WithIncidentAnalyzer(a *incidents.Analyzer) GetIncidentReportOption {
	return func(h *GetIncidentReportHandler) {
		h.analyzer = a
	}
}

// WithIncidentExplainer sets the human explainer.
func WithIncidentExplainer(e *incidents.Explainer) GetIncidentReportOption {
	return func(h *GetIncidentReportHandler) {
		h.explainer = e
	}
}

// WithIncidentNamespace sets the default namespace.
func WithIncidentNamespace(ns string) GetIncidentReportOption {
	return func(h *GetIncidentReportHandler) {
		if ns != "" {
			h.namespace = ns
		}
	}
}

// NewGetIncidentReportHandler creates a new get_incident_report handler.
func NewGetIncidentReportHandler(
	k8sClientset kubernetes.Interface,
	opts ...GetIncidentReportOption,
) (*GetIncidentReportHandler, error) {
	explainer, err := incidents.NewExplainer()
	if err != nil {
		return nil, fmt.Errorf("create explainer: %w", err)
	}

	h := &GetIncidentReportHandler{
		k8sClientset:  k8sClientset,
		namespace:     "default",
		analyzer:      incidents.NewAnalyzer(),
		explainer:     explainer,
		incidentCache: make(map[string]*events.CorrelatedIncident),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

// CacheIncident stores an incident for later lookup by ID.
func (h *GetIncidentReportHandler) CacheIncident(incident *events.CorrelatedIncident) {
	if incident == nil || incident.ID == "" {
		return
	}
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	h.incidentCache[incident.ID] = incident
}

// Handle processes the get_incident_report tool request.
func (h *GetIncidentReportHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("get_incident_report invoked")

	// 1. Parse arguments
	args, err := h.parseReportArgs(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	klog.V(4).InfoS("get_incident_report args",
		"incident_id", args.incidentID,
		"pod_name", args.podName,
		"namespace", args.namespace,
		"include_snapshots", args.includeSnapshots,
		"include_raw_events", args.includeRawEvents,
	)

	// 2. Look up the incident
	incident, err := h.lookupIncident(ctx, args.incidentID, args.podName, args.namespace)
	if err != nil {
		klog.ErrorS(err, "failed to lookup incident")
		return mcp.NewToolResultError(err.Error()), nil
	}

	// 3. Analyze root cause
	if h.analyzer == nil {
		err := fmt.Errorf("incident analyzer is not configured")
		klog.ErrorS(err, "unable to analyze incident")
		return mcp.NewToolResultError(err.Error()), nil
	}
	report := h.analyzer.Analyze(incident)

	// 4. Generate explanation
	if h.explainer == nil {
		err := fmt.Errorf("incident explainer is not configured")
		klog.ErrorS(err, "unable to generate incident explanation")
		return mcp.NewToolResultError(err.Error()), nil
	}
	explanation := h.explainer.GenerateExplanation(incident)

	// 5. Build response with optional data
	response := h.buildReportResponse(incident, report, explanation, args)

	// 6. Cache the incident for future lookups
	h.CacheIncident(incident)

	klog.InfoS("get_incident_report completed",
		"incident_id", incident.ID,
		"root_cause", report.RootCause.Category,
	)

	return h.marshalReportResponse(response)
}

// reportArgs holds parsed arguments for get_incident_report.
type reportArgs struct {
	incidentID       string
	podName          string
	namespace        string
	includeSnapshots bool
	includeRawEvents bool
}

// parseReportArgs extracts and validates arguments from the request.
func (h *GetIncidentReportHandler) parseReportArgs(
	request mcp.CallToolRequest,
) (*reportArgs, error) {
	result := &reportArgs{
		namespace:        h.namespace,
		includeSnapshots: true,  // default: true per spec
		includeRawEvents: false, // default: false per spec
	}

	args := request.GetArguments()

	// incident_id (optional)
	if idRaw, ok := args["incident_id"]; ok && idRaw != nil {
		if id, ok := idRaw.(string); ok && id != "" {
			result.incidentID = id
		}
	}

	// pod_name (optional, alternative to incident_id)
	if podRaw, ok := args["pod_name"]; ok && podRaw != nil {
		if pod, ok := podRaw.(string); ok && pod != "" {
			result.podName = pod
		}
	}

	// Validate: need at least one lookup method
	if result.incidentID == "" && result.podName == "" {
		return nil, fmt.Errorf("either incident_id or pod_name is required")
	}

	// namespace (optional, for pod_name lookups)
	if nsRaw, ok := args["namespace"]; ok && nsRaw != nil {
		if ns, ok := nsRaw.(string); ok && ns != "" {
			result.namespace = ns
		}
	}

	// include_snapshots (optional, default: true)
	if snapRaw, ok := args["include_snapshots"]; ok && snapRaw != nil {
		if snap, ok := snapRaw.(bool); ok {
			result.includeSnapshots = snap
		}
	}

	// include_raw_events (optional, default: false)
	if rawRaw, ok := args["include_raw_events"]; ok && rawRaw != nil {
		if raw, ok := rawRaw.(bool); ok {
			result.includeRawEvents = raw
		}
	}

	return result, nil
}

// lookupIncident finds an incident by ID or pod_name.
func (h *GetIncidentReportHandler) lookupIncident(
	ctx context.Context,
	incidentID, podName, namespace string,
) (*events.CorrelatedIncident, error) {
	// Prefer incident_id lookup if provided
	if incidentID != "" {
		return h.lookupByID(incidentID)
	}

	// Fall back to pod_name lookup
	if podName != "" {
		return h.lookupByPod(ctx, podName, namespace)
	}

	return nil, fmt.Errorf("either incident_id or pod_name is required")
}

// lookupByID retrieves an incident from the cache.
func (h *GetIncidentReportHandler) lookupByID(id string) (*events.CorrelatedIncident, error) {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()

	incident, ok := h.incidentCache[id]
	if !ok {
		return nil, fmt.Errorf("incident not found: %s (try using pod_name instead)", id)
	}
	return incident, nil
}

// lookupByPod correlates events for a pod to build an incident.
func (h *GetIncidentReportHandler) lookupByPod(
	ctx context.Context,
	podName, namespace string,
) (*events.CorrelatedIncident, error) {
	// Get the pod
	pod, err := h.k8sClientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("pod not found: %s/%s: %w", namespace, podName, err)
	}

	// Check for failure and extract trigger
	failure := ExtractPodFailure(pod)
	if failure == nil {
		return nil, fmt.Errorf("pod %s/%s has no recorded failure", namespace, podName)
	}

	// Build trigger event
	trigger := events.Event{
		Type:      "k8s",
		Timestamp: failure.FailureTs,
		Source:    "get_incident_report",
		Data: events.K8sEvent{
			Timestamp: failure.FailureTs,
			Type:      "Warning",
			Reason:    failure.Reason,
			Message:   failure.Message,
			PodName:   pod.Name,
			Namespace: pod.Namespace,
			PodUID:    string(pod.UID),
			NodeName:  pod.Spec.NodeName,
		},
	}

	// Correlate if correlator available
	if h.correlator != nil {
		return h.correlator.Correlate(ctx, trigger), nil
	}

	// Build minimal incident without correlation
	return &events.CorrelatedIncident{
		ID:        fmt.Sprintf("report-%d", time.Now().UnixNano()),
		Timestamp: failure.FailureTs,
		Trigger:   trigger,
		AffectedPods: []events.AffectedPod{{
			PodName:   pod.Name,
			Namespace: pod.Namespace,
			PodUID:    string(pod.UID),
			Reason:    failure.Reason,
		}},
		Timeline: []events.TimelineEntry{{
			Timestamp:    failure.FailureTs,
			RelativeTime: "0s",
			EventType:    "k8s",
			Description:  fmt.Sprintf("[%s] %s", failure.Reason, failure.Message),
			Severity:     "critical",
		}},
		Causality: events.CausalityUnknown,
	}, nil
}

// IncidentReportResponse is the full response from get_incident_report.
type IncidentReportResponse struct {
	IncidentID       string                     `json:"incident_id"`
	Timestamp        string                     `json:"timestamp"`
	DurationAnalyzed string                     `json:"duration_analyzed"`
	Pod              IncidentPodInfo            `json:"pod"`
	GPU              *IncidentGPUInfo           `json:"gpu,omitempty"`
	RootCause        IncidentRootCause          `json:"root_cause"`
	Timeline         []IncidentTimelineEntry    `json:"timeline"`
	GPUSnapshots     []blackbox.GPUSnapshot     `json:"gpu_snapshots,omitempty"`
	K8sEvents        []events.K8sEvent          `json:"k8s_events,omitempty"`
	XIDEvents        []xid.XIDEvent             `json:"xid_events,omitempty"`
	Recommendations  []incidents.Recommendation `json:"recommendations"`
	Explanation      string                     `json:"explanation"`
}

// IncidentPodInfo contains full pod details.
type IncidentPodInfo struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	UID               string `json:"uid"`
	Node              string `json:"node"`
	StartTime         string `json:"start_time,omitempty"`
	FailureTime       string `json:"failure_time,omitempty"`
	ExitCode          int    `json:"exit_code,omitempty"`
	TerminationReason string `json:"termination_reason,omitempty"`
}

// IncidentGPUInfo contains full GPU details.
type IncidentGPUInfo struct {
	UUID     string `json:"uuid"`
	Index    int    `json:"index"`
	Name     string `json:"name,omitempty"`
	PCIBusID string `json:"pci_bus_id,omitempty"`
}

// IncidentRootCause contains detailed root cause analysis.
type IncidentRootCause struct {
	Category    string   `json:"category"`
	Confidence  float64  `json:"confidence"`
	NotYourCode bool     `json:"not_your_code"`
	Evidence    []string `json:"evidence"`
}

// IncidentTimelineEntry is a full timeline entry.
type IncidentTimelineEntry struct {
	Timestamp    string                 `json:"timestamp"`
	RelativeTime string                 `json:"relative"`
	EventType    string                 `json:"event_type"`
	Severity     string                 `json:"severity"`
	Description  string                 `json:"description"`
	Details      map[string]interface{} `json:"details,omitempty"`
}

// calculateAnalysisDuration computes the time span covered by the incident timeline.
// Returns the duration between first and last timeline events, or "30m" default
// if the timeline has fewer than 2 entries.
func calculateAnalysisDuration(incident *events.CorrelatedIncident) string {
	if len(incident.Timeline) < 2 {
		return "30m" // default analysis window
	}

	first := incident.Timeline[0].Timestamp
	last := incident.Timeline[len(incident.Timeline)-1].Timestamp

	if first.IsZero() || last.IsZero() {
		return "30m"
	}

	duration := last.Sub(first)
	if duration <= 0 {
		return "30m"
	}

	// Round to appropriate unit for readability
	switch {
	case duration < time.Minute:
		return duration.Round(time.Second).String()
	case duration < time.Hour:
		return duration.Round(time.Minute).String()
	default:
		return duration.Round(time.Minute).String()
	}
}

// buildReportResponse constructs the comprehensive response.
func (h *GetIncidentReportHandler) buildReportResponse(
	incident *events.CorrelatedIncident,
	report *incidents.IncidentReport,
	explanation string,
	args *reportArgs,
) IncidentReportResponse {
	resp := IncidentReportResponse{
		IncidentID:       incident.ID,
		Timestamp:        incident.Timestamp.Format(time.RFC3339),
		DurationAnalyzed: calculateAnalysisDuration(incident),
		RootCause: IncidentRootCause{
			Category:    report.RootCause.Category,
			Confidence:  report.RootCause.Confidence,
			NotYourCode: report.RootCause.NotYourCode,
			Evidence:    report.RootCause.Evidence,
		},
		Recommendations: report.Recommendations,
		Explanation:     explanation,
	}

	// Build pod info from first affected pod
	if len(incident.AffectedPods) > 0 {
		pod := incident.AffectedPods[0]
		resp.Pod = IncidentPodInfo{
			Name:              pod.PodName,
			Namespace:         pod.Namespace,
			UID:               pod.PodUID,
			TerminationReason: pod.Reason,
		}
		// Extract node from trigger if it's a K8s event
		if incident.Trigger.Type == "k8s" {
			if k8sEvent, ok := incident.Trigger.Data.(events.K8sEvent); ok {
				resp.Pod.Node = k8sEvent.NodeName
			}
		}
	}

	// Add GPU info if available
	if report.GPUUUID != "" {
		resp.GPU = &IncidentGPUInfo{
			UUID: report.GPUUUID,
		}
	}

	// Build full timeline
	resp.Timeline = make([]IncidentTimelineEntry, 0, len(incident.Timeline))
	for _, e := range incident.Timeline {
		entry := IncidentTimelineEntry{
			Timestamp:    e.Timestamp.Format(time.RFC3339),
			RelativeTime: e.RelativeTime,
			EventType:    e.EventType,
			Severity:     e.Severity,
			Description:  e.Description,
		}
		resp.Timeline = append(resp.Timeline, entry)
	}

	// Include GPU snapshots if requested
	if args.includeSnapshots {
		if h.recorder != nil {
			// Get snapshots from the flight recorder
			snapshots := h.recorder.GetAllTimelines(30 * time.Minute)
			for _, timeline := range snapshots {
				resp.GPUSnapshots = append(resp.GPUSnapshots, timeline...)
			}
		} else if len(incident.GPUSnapshots) > 0 {
			// Use snapshots from the incident itself
			resp.GPUSnapshots = incident.GPUSnapshots
		}
	}

	// Include raw events if requested
	if args.includeRawEvents {
		resp.K8sEvents, resp.XIDEvents = h.extractRawEvents(incident)
	}

	return resp
}

// extractRawEvents extracts K8s and XID events from the incident's related events.
func (h *GetIncidentReportHandler) extractRawEvents(
	incident *events.CorrelatedIncident,
) ([]events.K8sEvent, []xid.XIDEvent) {
	var k8sEvents []events.K8sEvent
	var xidEvents []xid.XIDEvent

	// Include trigger if it's a K8s or XID event
	switch incident.Trigger.Type {
	case "k8s":
		if k8sEvent, ok := incident.Trigger.Data.(events.K8sEvent); ok {
			k8sEvents = append(k8sEvents, k8sEvent)
		}
	case "xid":
		if xidEvent, ok := incident.Trigger.Data.(xid.XIDEvent); ok {
			xidEvents = append(xidEvents, xidEvent)
		}
	}

	// Extract from related events
	for _, e := range incident.RelatedEvents {
		switch e.Type {
		case "k8s":
			if k8sEvent, ok := e.Data.(events.K8sEvent); ok {
				k8sEvents = append(k8sEvents, k8sEvent)
			}
		case "xid":
			if xidEvent, ok := e.Data.(xid.XIDEvent); ok {
				xidEvents = append(xidEvents, xidEvent)
			}
		}
	}

	return k8sEvents, xidEvents
}

// marshalReportResponse marshals the response to JSON.
func (h *GetIncidentReportHandler) marshalReportResponse(
	response IncidentReportResponse,
) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// GetIncidentReportTool returns the MCP tool definition for get_incident_report.
func GetIncidentReportTool() mcp.Tool {
	return mcp.NewTool("get_incident_report",
		mcp.WithDescription(
			"Get detailed incident report with full timeline, hardware snapshots, "+
				"and correlated events. More detailed than explain_failure. "+
				"Use this for deep debugging, post-mortems, or custom analysis.",
		),
		mcp.WithString("incident_id",
			mcp.Description("Incident ID from previous explain_failure call"),
		),
		mcp.WithString("pod_name",
			mcp.Description("Pod name (alternative to incident_id)"),
		),
		mcp.WithString("namespace",
			mcp.Description("Kubernetes namespace"),
		),
		mcp.WithBoolean("include_snapshots",
			mcp.Description("Include full GPU snapshots (default: true)"),
		),
		mcp.WithBoolean("include_raw_events",
			mcp.Description("Include raw K8s and XID events (default: false)"),
		),
	)
}
