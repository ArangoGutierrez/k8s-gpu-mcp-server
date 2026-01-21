// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/incidents"
	"github.com/mark3labs/mcp-go/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// extractTextContent safely extracts text content from a tool result.
// It uses the comma-ok idiom to avoid panics on type assertion failures.
func extractTextContent(t *testing.T, result *mcp.CallToolResult) string {
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

func TestExplainFailureHandler_Handle_PodNotFound(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewExplainFailureHandler(clientset)

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

func TestExplainFailureHandler_Handle_PodNotFailed(t *testing.T) {
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
	handler := NewExplainFailureHandler(clientset)

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
		t.Error("expected error result for running pod")
	}
}

func TestExplainFailureHandler_Handle_FailedPod(t *testing.T) {
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
	handler := NewExplainFailureHandler(clientset)

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

	// Verify response structure
	var response ExplainFailureResponse
	text := extractTextContent(t, result)
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.Pod.Name != "failed-pod" {
		t.Errorf("expected pod name 'failed-pod', got %s", response.Pod.Name)
	}
	if response.Pod.Node != "gpu-node-1" {
		t.Errorf("expected node 'gpu-node-1', got %s", response.Pod.Node)
	}
	if response.RootCause.Category == "" {
		t.Error("expected non-empty root cause category")
	}
	if response.Explanation == "" {
		t.Error("expected non-empty explanation")
	}
}

func TestExplainFailureHandler_Handle_OOMKilled(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oom-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "main",
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode:   137,
						Reason:     "OOMKilled",
						FinishedAt: metav1.Time{Time: time.Now()},
					},
				},
			}},
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(pod)
	handler := NewExplainFailureHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name": "oom-pod",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Errorf("unexpected error result: %v", result)
	}

	// Verify OOMKilled is captured
	var response ExplainFailureResponse
	text := extractTextContent(t, result)
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.Pod.Name != "oom-pod" {
		t.Errorf("expected pod name 'oom-pod', got %s", response.Pod.Name)
	}
}

func TestExplainFailureHandler_Handle_TerminatedContainer(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "terminated-pod",
			Namespace: "gpu-workloads",
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-2",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "trainer",
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode:   1,
						Reason:     "Error",
						Message:    "CUDA out of memory",
						FinishedAt: metav1.Time{Time: time.Now()},
					},
				},
			}},
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(pod)
	handler := NewExplainFailureHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name":  "terminated-pod",
		"namespace": "gpu-workloads",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Errorf("unexpected error result: %v", result)
	}

	var response ExplainFailureResponse
	text := extractTextContent(t, result)
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.Pod.Namespace != "gpu-workloads" {
		t.Errorf("expected namespace 'gpu-workloads', got %s", response.Pod.Namespace)
	}
	if response.Pod.Container != "trainer" {
		t.Errorf("expected container 'trainer', got %s", response.Pod.Container)
	}
}

func TestExplainFailureHandler_Handle_FailedInitContainer(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "init-failed-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{{
				Name: "setup-gpu",
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode:   1,
						Reason:     "Error",
						Message:    "Failed to initialize GPU",
						FinishedAt: metav1.Time{Time: time.Now()},
					},
				},
			}},
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(pod)
	handler := NewExplainFailureHandler(clientset)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name": "init-failed-pod",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Errorf("unexpected error result: %v", result)
	}

	var response ExplainFailureResponse
	text := extractTextContent(t, result)
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.Pod.Name != "init-failed-pod" {
		t.Errorf("expected pod name 'init-failed-pod', got %s", response.Pod.Name)
	}
	if response.Pod.Container != "setup-gpu" {
		t.Errorf("expected container 'setup-gpu', got %s", response.Pod.Container)
	}
}

func TestExplainFailureHandler_parseArgs_RequiredPodName(t *testing.T) {
	handler := NewExplainFailureHandler(nil)

	tests := []struct {
		name    string
		args    map[string]any
		wantErr bool
	}{
		{
			name:    "missing pod_name",
			args:    map[string]any{},
			wantErr: true,
		},
		{
			name:    "nil pod_name",
			args:    map[string]any{"pod_name": nil},
			wantErr: true,
		},
		{
			name:    "empty pod_name",
			args:    map[string]any{"pod_name": ""},
			wantErr: true,
		},
		{
			name:    "valid pod_name",
			args:    map[string]any{"pod_name": "my-pod"},
			wantErr: false,
		},
		{
			name:    "with custom namespace",
			args:    map[string]any{"pod_name": "my-pod", "namespace": "production"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = tt.args

			podName, namespace, err := handler.parseArgs(request)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseArgs() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				if podName == "" {
					t.Error("expected non-empty pod_name")
				}
				if namespace == "" {
					t.Error("expected non-empty namespace (default)")
				}

				// Verify custom namespace
				if ns, ok := tt.args["namespace"].(string); ok && ns != "" {
					if namespace != ns {
						t.Errorf("expected namespace %s, got %s", ns, namespace)
					}
				}
			}
		})
	}
}

func TestExplainFailureHandler_extractPodFailure(t *testing.T) {
	handler := NewExplainFailureHandler(nil)

	tests := []struct {
		name          string
		pod           *corev1.Pod
		wantFailure   bool
		wantReason    string
		wantContainer string // expected container name (empty for pod-level failures)
	}{
		{
			name: "PodFailed phase has no container",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase:   corev1.PodFailed,
					Reason:  "DeadlineExceeded",
					Message: "Job exceeded deadline",
				},
			},
			wantFailure:   true,
			wantReason:    "DeadlineExceeded",
			wantContainer: "", // Pod-level failure has no container
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
			name: "terminated container captures name",
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
			name: "failed init container captures name",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					InitContainerStatuses: []corev1.ContainerStatus{{
						Name: "init-nvidia",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Reason:   "Error",
								Message:  "nvidia-smi not found",
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
			name: "successful init container is not failure",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					InitContainerStatuses: []corev1.ContainerStatus{{
						Name: "init-setup",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 0,
								Reason:   "Completed",
							},
						},
					}},
				},
			},
			wantFailure: false,
		},
		{
			name: "OOMKilled captures container name",
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
		{
			name: "successful termination is not failure",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "job",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 0,
								Reason:   "Completed",
							},
						},
					}},
				},
			},
			wantFailure: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failure := handler.extractPodFailure(tt.pod)

			if tt.wantFailure && failure == nil {
				t.Error("expected failure, got nil")
			}
			if !tt.wantFailure && failure != nil {
				t.Errorf("expected no failure, got %+v", failure)
			}
			if failure != nil && failure.reason != tt.wantReason {
				t.Errorf("expected reason %s, got %s", tt.wantReason, failure.reason)
			}
			if failure != nil && failure.containerName != tt.wantContainer {
				t.Errorf("expected container %q, got %q", tt.wantContainer, failure.containerName)
			}
		})
	}
}

func TestGetConditionTime(t *testing.T) {
	now := time.Now()
	creationTime := now.Add(-1 * time.Hour)
	conditionTime := now.Add(-30 * time.Minute)

	tests := []struct {
		name     string
		pod      *corev1.Pod
		condType corev1.PodConditionType
		want     time.Time
		approx   bool // if true, check time is recent (within 1 second)
	}{
		{
			name: "condition exists",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{{
						Type:               corev1.PodReady,
						LastTransitionTime: metav1.Time{Time: conditionTime},
					}},
				},
			},
			condType: corev1.PodReady,
			want:     conditionTime,
		},
		{
			name: "condition missing fallback to creation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: creationTime},
				},
			},
			condType: corev1.PodReady,
			want:     creationTime,
		},
		{
			name: "different condition type returns creation time",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: creationTime},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{{
						Type:               corev1.PodScheduled,
						LastTransitionTime: metav1.Time{Time: conditionTime},
					}},
				},
			},
			condType: corev1.PodReady, // asking for Ready but only Scheduled exists
			want:     creationTime,
		},
		{
			name:     "no condition no creation returns now",
			pod:      &corev1.Pod{},
			condType: corev1.PodReady,
			approx:   true, // will be close to time.Now()
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getConditionTime(tt.pod, tt.condType)

			if tt.approx {
				// Check that returned time is within 1 second of now
				if time.Since(got) > time.Second {
					t.Errorf("getConditionTime() = %v, expected recent time", got)
				}
			} else {
				if !got.Equal(tt.want) {
					t.Errorf("getConditionTime() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestExplainFailureHandler_buildMinimalIncident(t *testing.T) {
	handler := NewExplainFailureHandler(nil)

	failure := &podFailure{
		pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-ns",
				UID:       "test-uid-123",
			},
		},
		failureTs: time.Now(),
		reason:    "OOMKilled",
		message:   "Memory limit exceeded",
	}

	trigger := handler.buildTriggerEvent(failure)
	incident := handler.buildMinimalIncident(trigger, failure)

	if incident == nil {
		t.Fatal("expected non-nil incident")
	}

	if !strings.HasPrefix(incident.ID, "manual-") {
		t.Errorf("expected ID to start with 'manual-', got %s", incident.ID)
	}

	if len(incident.AffectedPods) != 1 {
		t.Errorf("expected 1 affected pod, got %d", len(incident.AffectedPods))
	}

	if incident.AffectedPods[0].PodName != "test-pod" {
		t.Errorf("expected pod name 'test-pod', got %s", incident.AffectedPods[0].PodName)
	}

	if len(incident.Timeline) != 1 {
		t.Errorf("expected 1 timeline entry, got %d", len(incident.Timeline))
	}

	if incident.Timeline[0].RelativeTime != "0s" {
		t.Errorf("expected relative time '0s', got %s", incident.Timeline[0].RelativeTime)
	}
}

func TestExplainFailureHandler_buildResponse(t *testing.T) {
	handler := NewExplainFailureHandler(nil)

	failure := &podFailure{
		pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ml-training-pod",
				Namespace: "ml",
			},
			Spec: corev1.PodSpec{
				NodeName: "gpu-node-1",
			},
		},
		failureTs: time.Now(),
		reason:    "OOMKilled",
		message:   "Container exceeded memory limit",
	}

	trigger := handler.buildTriggerEvent(failure)
	incident := handler.buildMinimalIncident(trigger, failure)
	report := handler.analyzer.Analyze(incident)
	explanation := handler.explainer.GenerateExplanation(incident)

	response := handler.buildResponse(failure, incident, report, explanation)

	if response.Pod.Name != "ml-training-pod" {
		t.Errorf("expected pod name 'ml-training-pod', got %s", response.Pod.Name)
	}
	if response.Pod.Namespace != "ml" {
		t.Errorf("expected namespace 'ml', got %s", response.Pod.Namespace)
	}
	if response.Pod.Node != "gpu-node-1" {
		t.Errorf("expected node 'gpu-node-1', got %s", response.Pod.Node)
	}
	if response.RootCause.Category == "" {
		t.Error("expected non-empty root cause category")
	}
	if response.Explanation == "" {
		t.Error("expected non-empty explanation")
	}
	if len(response.Timeline) == 0 {
		t.Error("expected non-empty timeline")
	}
}

func TestGetExplainFailureTool(t *testing.T) {
	tool := GetExplainFailureTool()

	if tool.Name != "explain_failure" {
		t.Errorf("expected tool name 'explain_failure', got %s", tool.Name)
	}

	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	// Verify description mentions key features
	if !strings.Contains(tool.Description, "GPU") {
		t.Error("expected description to mention GPU")
	}
	if !strings.Contains(tool.Description, "root cause") {
		t.Error("expected description to mention root cause")
	}
	if !strings.Contains(tool.Description, "hardware") {
		t.Error("expected description to mention hardware")
	}
}

func TestExplainFailureHandler_WithOptions(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()

	t.Run("WithNamespace sets custom namespace", func(t *testing.T) {
		handler := NewExplainFailureHandler(clientset,
			WithNamespace("custom-ns"),
		)
		if handler.namespace != "custom-ns" {
			t.Errorf("expected namespace 'custom-ns', got %s", handler.namespace)
		}
	})

	t.Run("WithNamespace empty does not override default", func(t *testing.T) {
		handler := NewExplainFailureHandler(clientset,
			WithNamespace(""),
		)
		if handler.namespace != "default" {
			t.Errorf("expected namespace 'default', got %s", handler.namespace)
		}
	})

	t.Run("WithCorrelator sets correlator", func(t *testing.T) {
		// WithCorrelator(nil) should set correlator to nil
		handler := NewExplainFailureHandler(clientset,
			WithCorrelator(nil),
		)
		if handler.correlator != nil {
			t.Error("expected correlator to be nil")
		}
	})

	t.Run("WithAnalyzer sets custom analyzer", func(t *testing.T) {
		customAnalyzer := incidents.NewAnalyzer()
		handler := NewExplainFailureHandler(clientset,
			WithAnalyzer(customAnalyzer),
		)
		if handler.analyzer != customAnalyzer {
			t.Error("expected custom analyzer to be set")
		}
	})

	t.Run("WithAnalyzer nil sets nil", func(t *testing.T) {
		handler := NewExplainFailureHandler(clientset,
			WithAnalyzer(nil),
		)
		if handler.analyzer != nil {
			t.Error("expected analyzer to be nil")
		}
	})

	t.Run("WithExplainer sets custom explainer", func(t *testing.T) {
		customExplainer := incidents.NewExplainer()
		handler := NewExplainFailureHandler(clientset,
			WithExplainer(customExplainer),
		)
		if handler.explainer != customExplainer {
			t.Error("expected custom explainer to be set")
		}
	})

	t.Run("WithExplainer nil sets nil", func(t *testing.T) {
		handler := NewExplainFailureHandler(clientset,
			WithExplainer(nil),
		)
		if handler.explainer != nil {
			t.Error("expected explainer to be nil")
		}
	})

	t.Run("multiple options applied in order", func(t *testing.T) {
		customAnalyzer := incidents.NewAnalyzer()
		customExplainer := incidents.NewExplainer()

		handler := NewExplainFailureHandler(clientset,
			WithNamespace("prod"),
			WithAnalyzer(customAnalyzer),
			WithExplainer(customExplainer),
		)

		if handler.namespace != "prod" {
			t.Errorf("expected namespace 'prod', got %s", handler.namespace)
		}
		if handler.analyzer != customAnalyzer {
			t.Error("expected custom analyzer to be set")
		}
		if handler.explainer != customExplainer {
			t.Error("expected custom explainer to be set")
		}
	})

	t.Run("default values when no options", func(t *testing.T) {
		handler := NewExplainFailureHandler(clientset)

		if handler.namespace != "default" {
			t.Errorf("expected default namespace 'default', got %s", handler.namespace)
		}
		if handler.correlator != nil {
			t.Error("expected correlator to be nil by default")
		}
		if handler.analyzer == nil {
			t.Error("expected analyzer to be non-nil by default")
		}
		if handler.explainer == nil {
			t.Error("expected explainer to be non-nil by default")
		}
	})
}

func TestExplainFailureHandler_Handle_DefaultNamespace(t *testing.T) {
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
	handler := NewExplainFailureHandler(clientset)

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

func TestExplainFailureHandler_Handle_ContextCancellation(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	handler := NewExplainFailureHandler(clientset)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"pod_name": "any-pod",
	}

	result, err := handler.Handle(ctx, request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should fail due to cancelled context when trying to get pod
	if !result.IsError {
		t.Error("expected error result for cancelled context")
	}
}
