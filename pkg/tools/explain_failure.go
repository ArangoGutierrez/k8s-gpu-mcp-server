// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/incidents"
	"github.com/mark3labs/mcp-go/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// ExplainFailureHandler handles the explain_failure tool.
type ExplainFailureHandler struct {
	k8sClientset kubernetes.Interface
	correlator   *events.Correlator
	analyzer     *incidents.Analyzer
	explainer    *incidents.Explainer
	namespace    string
}

// ExplainFailureOption configures ExplainFailureHandler.
type ExplainFailureOption func(*ExplainFailureHandler)

// WithCorrelator sets the event correlator.
func WithCorrelator(c *events.Correlator) ExplainFailureOption {
	return func(h *ExplainFailureHandler) {
		h.correlator = c
	}
}

// WithAnalyzer sets the incident analyzer.
func WithAnalyzer(a *incidents.Analyzer) ExplainFailureOption {
	return func(h *ExplainFailureHandler) {
		h.analyzer = a
	}
}

// WithExplainer sets the human explainer.
func WithExplainer(e *incidents.Explainer) ExplainFailureOption {
	return func(h *ExplainFailureHandler) {
		h.explainer = e
	}
}

// WithNamespace sets the default namespace.
func WithNamespace(ns string) ExplainFailureOption {
	return func(h *ExplainFailureHandler) {
		if ns != "" {
			h.namespace = ns
		}
	}
}

// NewExplainFailureHandler creates a new explain_failure handler.
func NewExplainFailureHandler(
	k8sClientset kubernetes.Interface,
	opts ...ExplainFailureOption,
) *ExplainFailureHandler {
	h := &ExplainFailureHandler{
		k8sClientset: k8sClientset,
		namespace:    "default",
		analyzer:     incidents.NewAnalyzer(),
		explainer:    incidents.NewExplainer(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// podFailure contains information about a failed pod.
type podFailure struct {
	pod           *corev1.Pod
	failureTs     time.Time
	reason        string
	message       string
	containerName string // Name of the failed container (empty for pod-level failures)
}

// findPodFailure locates the pod and extracts failure information.
func (h *ExplainFailureHandler) findPodFailure(
	ctx context.Context,
	podName, namespace string,
) (*podFailure, error) {
	pod, err := h.k8sClientset.CoreV1().Pods(namespace).Get(
		ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("pod not found: %s/%s: %w", namespace, podName, err)
	}

	// Check if pod is in a failed state
	failure := h.extractPodFailure(pod)
	if failure == nil {
		return nil, fmt.Errorf("pod %s/%s is not in a failed state (phase: %s)",
			namespace, podName, pod.Status.Phase)
	}

	return failure, nil
}

// extractPodFailure checks pod status for failure conditions.
func (h *ExplainFailureHandler) extractPodFailure(pod *corev1.Pod) *podFailure {
	// Check phase first
	if pod.Status.Phase == corev1.PodFailed {
		return &podFailure{
			pod:       pod,
			failureTs: getConditionTime(pod, corev1.PodReady),
			reason:    pod.Status.Reason,
			message:   pod.Status.Message,
		}
	}

	// Check init container statuses for terminated with error
	// Init container failures are common in GPU workloads (e.g., NVIDIA device plugin init)
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return &podFailure{
				pod:           pod,
				failureTs:     cs.State.Terminated.FinishedAt.Time,
				reason:        cs.State.Terminated.Reason,
				message:       cs.State.Terminated.Message,
				containerName: cs.Name,
			}
		}
	}

	// Check container statuses for terminated with error
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return &podFailure{
				pod:           pod,
				failureTs:     cs.State.Terminated.FinishedAt.Time,
				reason:        cs.State.Terminated.Reason,
				message:       cs.State.Terminated.Message,
				containerName: cs.Name,
			}
		}
	}

	// Check for OOMKilled in last termination state
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated != nil {
			term := cs.LastTerminationState.Terminated
			if term.Reason == "OOMKilled" || term.ExitCode != 0 {
				return &podFailure{
					pod:           pod,
					failureTs:     term.FinishedAt.Time,
					reason:        term.Reason,
					message:       term.Message,
					containerName: cs.Name,
				}
			}
		}
	}

	return nil
}

// getConditionTime returns the transition time for a condition.
// Falls back to pod creation time for reproducibility, or current time if neither is available.
func getConditionTime(pod *corev1.Pod, condType corev1.PodConditionType) time.Time {
	for _, c := range pod.Status.Conditions {
		if c.Type == condType {
			return c.LastTransitionTime.Time
		}
	}
	// Fallback to pod creation time for reproducibility
	if !pod.CreationTimestamp.IsZero() {
		return pod.CreationTimestamp.Time
	}
	return time.Now()
}

// Handle processes the explain_failure tool request.
func (h *ExplainFailureHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("explain_failure invoked")

	// 1. Parse arguments
	podName, namespace, err := h.parseArgs(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	klog.V(4).InfoS("explain_failure args",
		"pod", podName, "namespace", namespace)

	// 2. Find the pod and verify failure
	failure, err := h.findPodFailure(ctx, podName, namespace)
	if err != nil {
		klog.ErrorS(err, "failed to find pod failure")
		return mcp.NewToolResultError(err.Error()), nil
	}

	// 3. Build trigger event from pod failure
	trigger := h.buildTriggerEvent(failure)

	// 4. Correlate events (if correlator available)
	var incident *events.CorrelatedIncident
	if h.correlator != nil {
		incident = h.correlator.Correlate(ctx, trigger)
	} else {
		// Minimal incident without correlation
		incident = h.buildMinimalIncident(trigger, failure)
	}

	// 5. Analyze root cause
	report := h.analyzer.Analyze(incident)

	// 6. Generate human explanation
	explanation := h.explainer.GenerateExplanation(incident)

	// 7. Build and return response
	response := h.buildResponse(failure, incident, report, explanation)

	klog.InfoS("explain_failure completed",
		"pod", podName,
		"root_cause", report.RootCause.Category,
		"confidence", report.RootCause.Confidence,
	)

	return h.marshalResponse(response)
}

// parseArgs extracts and validates arguments from the request.
func (h *ExplainFailureHandler) parseArgs(
	request mcp.CallToolRequest,
) (podName, namespace string, err error) {
	args := request.GetArguments()

	// pod_name (required)
	podNameRaw, ok := args["pod_name"]
	if !ok || podNameRaw == nil {
		return "", "", fmt.Errorf("pod_name is required")
	}
	podName, ok = podNameRaw.(string)
	if !ok || podName == "" {
		return "", "", fmt.Errorf("pod_name must be a non-empty string")
	}

	// namespace (optional, default from handler)
	namespace = h.namespace
	if nsRaw, ok := args["namespace"]; ok && nsRaw != nil {
		if ns, ok := nsRaw.(string); ok && ns != "" {
			namespace = ns
		}
	}

	return podName, namespace, nil
}

// buildTriggerEvent creates a trigger Event from pod failure.
func (h *ExplainFailureHandler) buildTriggerEvent(failure *podFailure) events.Event {
	return events.Event{
		Type:      "k8s",
		Timestamp: failure.failureTs,
		Source:    "explain_failure",
		Data: events.K8sEvent{
			Timestamp: failure.failureTs,
			Type:      "Warning",
			Reason:    failure.reason,
			Message:   failure.message,
			PodName:   failure.pod.Name,
			Namespace: failure.pod.Namespace,
			PodUID:    string(failure.pod.UID),
			NodeName:  failure.pod.Spec.NodeName,
		},
	}
}

// buildMinimalIncident creates an incident without full correlation.
func (h *ExplainFailureHandler) buildMinimalIncident(
	trigger events.Event,
	failure *podFailure,
) *events.CorrelatedIncident {
	return &events.CorrelatedIncident{
		ID:        fmt.Sprintf("manual-%d", time.Now().UnixNano()),
		Timestamp: failure.failureTs,
		Trigger:   trigger,
		AffectedPods: []events.AffectedPod{{
			PodName:   failure.pod.Name,
			Namespace: failure.pod.Namespace,
			PodUID:    string(failure.pod.UID),
			Reason:    failure.reason,
		}},
		Timeline: []events.TimelineEntry{{
			Timestamp:    failure.failureTs,
			RelativeTime: "0s",
			EventType:    "k8s",
			Description:  fmt.Sprintf("[%s] %s", failure.reason, failure.message),
			Severity:     "critical",
		}},
		Causality: events.CausalityUnknown,
	}
}

// ExplainFailureResponse is the structured response from explain_failure.
type ExplainFailureResponse struct {
	Pod             PodSummary                  `json:"pod"`
	GPU             *GPUSummary                 `json:"gpu,omitempty"`
	RootCause       RootCauseSummary            `json:"root_cause"`
	Explanation     string                      `json:"explanation"`
	Timeline        []TimelineEntry             `json:"timeline"`
	Recommendations []incidents.Recommendation  `json:"recommendations"`
	HardwareState   *incidents.HardwareSnapshot `json:"hardware_state,omitempty"`
}

// PodSummary contains pod identification.
type PodSummary struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Node      string `json:"node"`
	Container string `json:"container,omitempty"` // Name of the failed container (if applicable)
}

// GPUSummary contains GPU identification.
type GPUSummary struct {
	UUID  string `json:"uuid"`
	Index int    `json:"index"`
	Name  string `json:"name,omitempty"`
}

// RootCauseSummary contains root cause analysis.
type RootCauseSummary struct {
	Category    string   `json:"category"`
	Confidence  float64  `json:"confidence"`
	NotYourCode bool     `json:"not_your_code"`
	Evidence    []string `json:"evidence"`
}

// TimelineEntry is a simplified timeline entry for the response.
type TimelineEntry struct {
	RelativeTime string `json:"t"`
	Event        string `json:"event"`
}

// buildResponse constructs the final response.
func (h *ExplainFailureHandler) buildResponse(
	failure *podFailure,
	incident *events.CorrelatedIncident,
	report *incidents.IncidentReport,
	explanation string,
) ExplainFailureResponse {
	resp := ExplainFailureResponse{
		Pod: PodSummary{
			Name:      failure.pod.Name,
			Namespace: failure.pod.Namespace,
			Node:      failure.pod.Spec.NodeName,
			Container: failure.containerName,
		},
		RootCause: RootCauseSummary{
			Category:    report.RootCause.Category,
			Confidence:  report.RootCause.Confidence,
			NotYourCode: report.RootCause.NotYourCode,
			Evidence:    report.RootCause.Evidence,
		},
		Explanation:     explanation,
		Recommendations: report.Recommendations,
		HardwareState:   report.HardwareState,
	}

	// Add GPU info if available
	if report.GPUUUID != "" {
		resp.GPU = &GPUSummary{
			UUID: report.GPUUUID,
		}
	}

	// Build simplified timeline
	resp.Timeline = make([]TimelineEntry, 0, len(incident.Timeline))
	for _, e := range incident.Timeline {
		resp.Timeline = append(resp.Timeline, TimelineEntry{
			RelativeTime: e.RelativeTime,
			Event:        e.Description,
		})
	}

	return resp
}

// marshalResponse marshals the response to JSON.
func (h *ExplainFailureHandler) marshalResponse(
	response ExplainFailureResponse,
) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// GetExplainFailureTool returns the MCP tool definition for explain_failure.
func GetExplainFailureTool() mcp.Tool {
	return mcp.NewTool("explain_failure",
		mcp.WithDescription(
			"Analyze why a GPU workload failed and provide human-readable "+
				"explanation with root cause analysis and actionable recommendations. "+
				"Returns whether the failure was due to hardware issues or user code.",
		),
		mcp.WithString("pod_name",
			mcp.Required(),
			mcp.Description("Name of the failed pod"),
		),
		mcp.WithString("namespace",
			mcp.Description("Kubernetes namespace (default: current namespace)"),
		),
	)
}
