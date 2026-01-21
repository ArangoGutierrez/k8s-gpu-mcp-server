// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

// PodFailure contains information about a failed pod.
// Used by both explain_failure and get_incident_report tools.
type PodFailure struct {
	Pod           *corev1.Pod
	FailureTs     time.Time
	Reason        string
	Message       string
	ContainerName string // Name of the failed container (empty for pod-level failures)
}

// ExtractPodFailure checks pod status for failure conditions.
// Returns nil if no failure is detected.
func ExtractPodFailure(pod *corev1.Pod) *PodFailure {
	// Check phase first
	if pod.Status.Phase == corev1.PodFailed {
		return &PodFailure{
			Pod:       pod,
			FailureTs: GetConditionTime(pod, corev1.PodReady),
			Reason:    pod.Status.Reason,
			Message:   pod.Status.Message,
		}
	}

	// Check init container statuses for terminated with error
	// Init container failures are common in GPU workloads (e.g., NVIDIA device plugin init)
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return &PodFailure{
				Pod:           pod,
				FailureTs:     cs.State.Terminated.FinishedAt.Time,
				Reason:        cs.State.Terminated.Reason,
				Message:       cs.State.Terminated.Message,
				ContainerName: cs.Name,
			}
		}
	}

	// Check container statuses for terminated with error
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return &PodFailure{
				Pod:           pod,
				FailureTs:     cs.State.Terminated.FinishedAt.Time,
				Reason:        cs.State.Terminated.Reason,
				Message:       cs.State.Terminated.Message,
				ContainerName: cs.Name,
			}
		}
	}

	// Check for OOMKilled in last termination state
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated != nil {
			term := cs.LastTerminationState.Terminated
			if term.Reason == "OOMKilled" || term.ExitCode != 0 {
				return &PodFailure{
					Pod:           pod,
					FailureTs:     term.FinishedAt.Time,
					Reason:        term.Reason,
					Message:       term.Message,
					ContainerName: cs.Name,
				}
			}
		}
	}

	return nil
}

// GetConditionTime returns the transition time for a pod condition.
// Falls back to pod creation time for reproducibility, or current time if neither is available.
func GetConditionTime(pod *corev1.Pod, condType corev1.PodConditionType) time.Time {
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
