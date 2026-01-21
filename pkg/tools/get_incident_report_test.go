// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/mark3labs/mcp-go/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetIncidentReportHandler_Handle_NeitherIDNorPod(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewGetIncidentReportHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result when neither incident_id nor pod_name provided")
	}
}

func TestGetIncidentReportHandler_Handle_InvalidIncidentID(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewGetIncidentReportHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"incident_id": "nonexistent-incident",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for nonexistent incident_id")
	}
}

func TestGetIncidentReportHandler_Handle_PodNotFound(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewGetIncidentReportHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name":  "nonexistent-pod",
		"namespace": "default",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for nonexistent pod")
	}
}

func TestGetIncidentReportHandler_Handle_ByIncidentID(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewGetIncidentReportHandler(clientset)

	// Pre-cache an incident
	incident := &events.CorrelatedIncident{
		ID:        "test-incident-123",
		Timestamp: time.Now(),
		Trigger: events.Event{
			Type:      "k8s",
			Timestamp: time.Now(),
			Data: events.K8sEvent{
				PodName:   "test-pod",
				Namespace: "default",
				NodeName:  "node-1",
			},
		},
		AffectedPods: []events.AffectedPod{{
			PodName:   "test-pod",
			Namespace: "default",
			PodUID:    "uid-123",
		}},
		Timeline: []events.TimelineEntry{{
			Timestamp:    time.Now(),
			RelativeTime: "0s",
			EventType:    "k8s",
			Description:  "Pod failed",
			Severity:     "critical",
		}},
	}
	handler.CacheIncident(incident)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"incident_id": "test-incident-123",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Errorf("unexpected error result: %v", result)
	}

	// Verify response structure
	text := extractReportTextContent(t, result)
	var response IncidentReportResponse
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.IncidentID != "test-incident-123" {
		t.Errorf("expected incident_id 'test-incident-123', got %s", response.IncidentID)
	}
	if response.Pod.Name != "test-pod" {
		t.Errorf("expected pod name 'test-pod', got %s", response.Pod.Name)
	}
}

func TestGetIncidentReportHandler_Handle_ByPodName(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "failed-pod",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-1",
		},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "Error",
			Message: "Container exited with code 1",
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(pod)
	handler := NewGetIncidentReportHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name":  "failed-pod",
		"namespace": "default",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Errorf("unexpected error result: %v", result)
	}

	// Verify response
	text := extractReportTextContent(t, result)
	var response IncidentReportResponse
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.Pod.Name != "failed-pod" {
		t.Errorf("expected pod name 'failed-pod', got %s", response.Pod.Name)
	}
	if response.Pod.Node != "gpu-node-1" {
		t.Errorf("expected node 'gpu-node-1', got %s", response.Pod.Node)
	}
}

func TestGetIncidentReportHandler_Handle_PodNotFailed(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "running-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(pod)
	handler := NewGetIncidentReportHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name":  "running-pod",
		"namespace": "default",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for running pod with no failure")
	}
}

func TestGetIncidentReportHandler_parseReportArgs_Defaults(t *testing.T) {
	handler := NewGetIncidentReportHandler(nil)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name": "test-pod",
	}

	args, err := handler.parseReportArgs(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !args.includeSnapshots {
		t.Error("expected include_snapshots to default to true")
	}

	if args.includeRawEvents {
		t.Error("expected include_raw_events to default to false")
	}

	if args.namespace != "default" {
		t.Errorf("expected namespace 'default', got %s", args.namespace)
	}
}

func TestGetIncidentReportHandler_parseReportArgs_CustomFlags(t *testing.T) {
	handler := NewGetIncidentReportHandler(nil)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"incident_id":        "inc-123",
		"include_snapshots":  false,
		"include_raw_events": true,
	}

	args, err := handler.parseReportArgs(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if args.includeSnapshots {
		t.Error("expected include_snapshots to be false")
	}

	if !args.includeRawEvents {
		t.Error("expected include_raw_events to be true")
	}
}

func TestGetIncidentReportHandler_parseReportArgs_Validation(t *testing.T) {
	handler := NewGetIncidentReportHandler(nil)

	tests := []struct {
		name    string
		args    map[string]any
		wantErr bool
	}{
		{
			name:    "neither incident_id nor pod_name",
			args:    map[string]any{},
			wantErr: true,
		},
		{
			name:    "empty incident_id and pod_name",
			args:    map[string]any{"incident_id": "", "pod_name": ""},
			wantErr: true,
		},
		{
			name:    "valid incident_id",
			args:    map[string]any{"incident_id": "inc-123"},
			wantErr: false,
		},
		{
			name:    "valid pod_name",
			args:    map[string]any{"pod_name": "my-pod"},
			wantErr: false,
		},
		{
			name:    "both incident_id and pod_name",
			args:    map[string]any{"incident_id": "inc-123", "pod_name": "my-pod"},
			wantErr: false, // Both provided is valid (incident_id takes precedence)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = tt.args

			_, err := handler.parseReportArgs(request)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseReportArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetIncidentReportTool(t *testing.T) {
	tool := GetIncidentReportTool()

	if tool.Name != "get_incident_report" {
		t.Errorf("expected tool name 'get_incident_report', got %s", tool.Name)
	}

	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	// Verify description mentions key features
	if !strings.Contains(tool.Description, "detailed") {
		t.Error("expected description to mention 'detailed'")
	}
	if !strings.Contains(tool.Description, "timeline") {
		t.Error("expected description to mention 'timeline'")
	}
}

func TestGetIncidentReportHandler_CacheIncident(t *testing.T) {
	handler := NewGetIncidentReportHandler(nil)

	incident := &events.CorrelatedIncident{
		ID:        "cache-test-id",
		Timestamp: time.Now(),
	}

	handler.CacheIncident(incident)

	// Verify it can be retrieved
	retrieved, err := handler.lookupByID("cache-test-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved.ID != incident.ID {
		t.Errorf("expected ID %s, got %s", incident.ID, retrieved.ID)
	}
}

func TestGetIncidentReportHandler_CacheIncident_Nil(t *testing.T) {
	handler := NewGetIncidentReportHandler(nil)

	// Should not panic
	handler.CacheIncident(nil)

	// Should not panic
	handler.CacheIncident(&events.CorrelatedIncident{ID: ""})
}

func TestGetIncidentReportHandler_lookupByID_NotFound(t *testing.T) {
	handler := NewGetIncidentReportHandler(nil)

	_, err := handler.lookupByID("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent incident")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to mention 'not found', got: %v", err)
	}
}

func TestGetIncidentReportHandler_WithOptions(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()

	t.Run("WithIncidentNamespace sets custom namespace", func(t *testing.T) {
		handler := NewGetIncidentReportHandler(clientset,
			WithIncidentNamespace("custom-ns"),
		)
		if handler.namespace != "custom-ns" {
			t.Errorf("expected namespace 'custom-ns', got %s", handler.namespace)
		}
	})

	t.Run("WithIncidentNamespace empty does not override default", func(t *testing.T) {
		handler := NewGetIncidentReportHandler(clientset,
			WithIncidentNamespace(""),
		)
		if handler.namespace != "default" {
			t.Errorf("expected namespace 'default', got %s", handler.namespace)
		}
	})

	t.Run("WithIncidentCorrelator sets correlator", func(t *testing.T) {
		handler := NewGetIncidentReportHandler(clientset,
			WithIncidentCorrelator(nil),
		)
		if handler.correlator != nil {
			t.Error("expected correlator to be nil")
		}
	})

	t.Run("WithIncidentRecorder sets recorder", func(t *testing.T) {
		handler := NewGetIncidentReportHandler(clientset,
			WithIncidentRecorder(nil),
		)
		if handler.recorder != nil {
			t.Error("expected recorder to be nil")
		}
	})

	t.Run("default values when no options", func(t *testing.T) {
		handler := NewGetIncidentReportHandler(clientset)

		if handler.namespace != "default" {
			t.Errorf("expected default namespace 'default', got %s", handler.namespace)
		}
		if handler.correlator != nil {
			t.Error("expected correlator to be nil by default")
		}
		if handler.recorder != nil {
			t.Error("expected recorder to be nil by default")
		}
		if handler.analyzer == nil {
			t.Error("expected analyzer to be non-nil by default")
		}
		if handler.explainer == nil {
			t.Error("expected explainer to be non-nil by default")
		}
	})
}

func TestExtractPodFailure(t *testing.T) {
	tests := []struct {
		name          string
		pod           *corev1.Pod
		wantFailure   bool
		wantReason    string
		wantContainer string
	}{
		{
			name: "PodFailed phase",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase:   corev1.PodFailed,
					Reason:  "DeadlineExceeded",
					Message: "Job exceeded deadline",
				},
			},
			wantFailure:   true,
			wantReason:    "DeadlineExceeded",
			wantContainer: "",
		},
		{
			name: "running pod no failure",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			wantFailure: false,
		},
		{
			name: "terminated container",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "worker",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Reason:   "Error",
							},
						},
					}},
				},
			},
			wantFailure:   true,
			wantReason:    "Error",
			wantContainer: "worker",
		},
		{
			name: "failed init container",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					InitContainerStatuses: []corev1.ContainerStatus{{
						Name: "init-nvidia",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Reason:   "Error",
							},
						},
					}},
				},
			},
			wantFailure:   true,
			wantReason:    "Error",
			wantContainer: "init-nvidia",
		},
		{
			name: "OOMKilled in last termination state",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "ml-trainer",
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 137,
								Reason:   "OOMKilled",
							},
						},
					}},
				},
			},
			wantFailure:   true,
			wantReason:    "OOMKilled",
			wantContainer: "ml-trainer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failure := ExtractPodFailure(tt.pod)

			if tt.wantFailure && failure == nil {
				t.Error("expected failure, got nil")
			}
			if !tt.wantFailure && failure != nil {
				t.Errorf("expected no failure, got %+v", failure)
			}
			if failure != nil && failure.Reason != tt.wantReason {
				t.Errorf("expected reason %s, got %s", tt.wantReason, failure.Reason)
			}
			if failure != nil && failure.ContainerName != tt.wantContainer {
				t.Errorf("expected container %q, got %q", tt.wantContainer, failure.ContainerName)
			}
		})
	}
}

func TestCalculateAnalysisDuration(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		incident *events.CorrelatedIncident
		want     string
	}{
		{
			name: "empty timeline returns default",
			incident: &events.CorrelatedIncident{
				Timeline: []events.TimelineEntry{},
			},
			want: "30m",
		},
		{
			name: "single entry returns default",
			incident: &events.CorrelatedIncident{
				Timeline: []events.TimelineEntry{
					{Timestamp: now},
				},
			},
			want: "30m",
		},
		{
			name: "5 minute duration",
			incident: &events.CorrelatedIncident{
				Timeline: []events.TimelineEntry{
					{Timestamp: now},
					{Timestamp: now.Add(5 * time.Minute)},
				},
			},
			want: "5m0s",
		},
		{
			name: "90 second duration",
			incident: &events.CorrelatedIncident{
				Timeline: []events.TimelineEntry{
					{Timestamp: now},
					{Timestamp: now.Add(90 * time.Second)},
				},
			},
			want: "2m0s", // rounds to minutes
		},
		{
			name: "30 second duration",
			incident: &events.CorrelatedIncident{
				Timeline: []events.TimelineEntry{
					{Timestamp: now},
					{Timestamp: now.Add(30 * time.Second)},
				},
			},
			want: "30s",
		},
		{
			name: "2 hour duration",
			incident: &events.CorrelatedIncident{
				Timeline: []events.TimelineEntry{
					{Timestamp: now},
					{Timestamp: now.Add(2 * time.Hour)},
				},
			},
			want: "2h0m0s",
		},
		{
			name: "zero timestamps return default",
			incident: &events.CorrelatedIncident{
				Timeline: []events.TimelineEntry{
					{Timestamp: time.Time{}},
					{Timestamp: time.Time{}},
				},
			},
			want: "30m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateAnalysisDuration(tt.incident)
			if got != tt.want {
				t.Errorf("calculateAnalysisDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetIncidentReportHandler_extractRawEvents(t *testing.T) {
	handler := NewGetIncidentReportHandler(nil)

	incident := &events.CorrelatedIncident{
		Trigger: events.Event{
			Type: "k8s",
			Data: events.K8sEvent{
				PodName:   "trigger-pod",
				Namespace: "default",
			},
		},
		RelatedEvents: []events.Event{
			{
				Type: "k8s",
				Data: events.K8sEvent{
					PodName:   "related-pod",
					Namespace: "default",
				},
			},
		},
	}

	k8sEvents, xidEvents := handler.extractRawEvents(incident)

	if len(k8sEvents) != 2 {
		t.Errorf("expected 2 K8s events, got %d", len(k8sEvents))
	}

	if len(xidEvents) != 0 {
		t.Errorf("expected 0 XID events, got %d", len(xidEvents))
	}
}

func TestGetIncidentReportHandler_Handle_DefaultNamespace(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-ns-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase:  corev1.PodFailed,
			Reason: "Error",
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(pod)
	handler := NewGetIncidentReportHandler(clientset)

	// Don't provide namespace - should use default
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name": "default-ns-pod",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Errorf("unexpected error result: %v", result)
	}
}

func TestGetIncidentReportHandler_Handle_NilAnalyzer(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewGetIncidentReportHandler(clientset,
		WithIncidentAnalyzer(nil), // Force nil
	)

	// Pre-cache an incident to bypass lookup
	incident := &events.CorrelatedIncident{
		ID:        "nil-analyzer-test",
		Timestamp: time.Now(),
		Trigger: events.Event{
			Type:      "k8s",
			Timestamp: time.Now(),
			Data: events.K8sEvent{
				PodName:   "test-pod",
				Namespace: "default",
			},
		},
		AffectedPods: []events.AffectedPod{{
			PodName:   "test-pod",
			Namespace: "default",
		}},
	}
	handler.CacheIncident(incident)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"incident_id": "nil-analyzer-test",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result when analyzer is nil")
	}
}

func TestGetIncidentReportHandler_Handle_NilExplainer(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewGetIncidentReportHandler(clientset,
		WithIncidentExplainer(nil), // Force nil
	)

	// Pre-cache an incident to bypass lookup
	incident := &events.CorrelatedIncident{
		ID:        "nil-explainer-test",
		Timestamp: time.Now(),
		Trigger: events.Event{
			Type:      "k8s",
			Timestamp: time.Now(),
			Data: events.K8sEvent{
				PodName:   "test-pod",
				Namespace: "default",
			},
		},
		AffectedPods: []events.AffectedPod{{
			PodName:   "test-pod",
			Namespace: "default",
		}},
	}
	handler.CacheIncident(incident)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"incident_id": "nil-explainer-test",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result when explainer is nil")
	}
}

// extractReportTextContent safely extracts text content from a tool result.
func extractReportTextContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content in result")
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return content.Text
}
