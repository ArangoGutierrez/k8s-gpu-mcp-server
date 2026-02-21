// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExtractPodFailure_AdditionalCases(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name          string
		pod           *corev1.Pod
		wantNil       bool
		wantReason    string
		wantContainer string
	}{
		{
			name: "pod-level failure (Failed phase)",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase:   corev1.PodFailed,
					Reason:  "Evicted",
					Message: "The node was low on resource: memory.",
				},
			},
			wantNil:    false,
			wantReason: "Evicted",
		},
		{
			name: "CrashLoopBackOff with terminated container",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "gpu-worker",
							RestartCount: 5,
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason:  "CrashLoopBackOff",
									Message: "back-off 5m0s restarting failed container",
								},
							},
							LastTerminationState: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   1,
									Reason:     "Error",
									Message:    "container crashed",
									FinishedAt: metav1.NewTime(now.Add(-30 * time.Second)),
								},
							},
						},
					},
				},
			},
			wantNil:       false,
			wantReason:    "Error",
			wantContainer: "gpu-worker",
		},
		{
			name: "init container failure",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "nvidia-init",
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   137,
									Reason:     "Error",
									Message:    "NVIDIA driver init failed",
									FinishedAt: metav1.NewTime(now.Add(-1 * time.Minute)),
								},
							},
						},
					},
				},
			},
			wantNil:       false,
			wantReason:    "Error",
			wantContainer: "nvidia-init",
		},
		{
			name: "OOMKilled in last termination state",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "training",
							RestartCount: 1,
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{
									StartedAt: metav1.NewTime(now),
								},
							},
							LastTerminationState: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   137,
									Reason:     "OOMKilled",
									Message:    "",
									FinishedAt: metav1.NewTime(now.Add(-10 * time.Second)),
								},
							},
						},
					},
				},
			},
			wantNil:       false,
			wantReason:    "OOMKilled",
			wantContainer: "training",
		},
		{
			name: "running pod with no failure",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:  "healthy",
							Ready: true,
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{
									StartedAt: metav1.NewTime(now.Add(-1 * time.Hour)),
								},
							},
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "succeeded pod (no failure)",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "job",
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   0,
									Reason:     "Completed",
									FinishedAt: metav1.NewTime(now),
								},
							},
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "multiple containers - first fails",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "sidecar",
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   1,
									Reason:     "Error",
									Message:    "sidecar crashed",
									FinishedAt: metav1.NewTime(now.Add(-5 * time.Second)),
								},
							},
						},
						{
							Name:  "main",
							Ready: true,
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{
									StartedAt: metav1.NewTime(now.Add(-1 * time.Hour)),
								},
							},
						},
					},
				},
			},
			wantNil:       false,
			wantReason:    "Error",
			wantContainer: "sidecar",
		},
		{
			name: "init container success followed by container failure",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "init",
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   0,
									Reason:     "Completed",
									FinishedAt: metav1.NewTime(now.Add(-1 * time.Minute)),
								},
							},
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "app",
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   2,
									Reason:     "Error",
									Message:    "segfault",
									FinishedAt: metav1.NewTime(now),
								},
							},
						},
					},
				},
			},
			wantNil:       false,
			wantReason:    "Error",
			wantContainer: "app",
		},
		{
			name: "pending pod with no container statuses",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractPodFailure(tt.pod)
			if tt.wantNil {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result, "expected non-nil PodFailure")
			assert.Equal(t, tt.pod, result.Pod)
			assert.Equal(t, tt.wantReason, result.Reason)
			if tt.wantContainer != "" {
				assert.Equal(t, tt.wantContainer, result.ContainerName)
			}
		})
	}
}

func TestExtractPodFailure_NilPod(t *testing.T) {
	// ExtractPodFailure should not panic on nil pod.
	// The function dereferences pod.Status.Phase, so nil will panic.
	// This test documents the current behavior.
	assert.Panics(t, func() {
		ExtractPodFailure(nil)
	}, "ExtractPodFailure(nil) should panic (documents current behavior)")
}

func TestGetConditionTime(t *testing.T) {
	fixedTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	creationTime := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		pod      *corev1.Pod
		condType corev1.PodConditionType
		wantTime time.Time
		useFuzzy bool // use approximate comparison for time.Now() fallback
	}{
		{
			name: "returns matching condition time",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:               corev1.PodReady,
							LastTransitionTime: metav1.NewTime(fixedTime),
						},
					},
				},
			},
			condType: corev1.PodReady,
			wantTime: fixedTime,
		},
		{
			name: "falls back to creation timestamp",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.NewTime(creationTime),
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:               corev1.PodScheduled,
							LastTransitionTime: metav1.NewTime(fixedTime),
						},
					},
				},
			},
			condType: corev1.PodReady,
			wantTime: creationTime,
		},
		{
			name: "falls back to time.Now when no condition or creation",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{},
			},
			condType: corev1.PodReady,
			useFuzzy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetConditionTime(tt.pod, tt.condType)
			if tt.useFuzzy {
				assert.WithinDuration(t, time.Now(), got, 2*time.Second,
					"expected time close to now")
			} else {
				assert.Equal(t, tt.wantTime, got)
			}
		})
	}
}
