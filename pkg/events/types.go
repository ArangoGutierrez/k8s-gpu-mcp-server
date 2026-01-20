// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package events provides Kubernetes event watching for GPU workload correlation.
//
// The K8sWatcher monitors Pod failures, OOMKills, and GPU-related events,
// storing them in a ring buffer for correlation with GPU telemetry from the
// Flight Recorder (pkg/blackbox).
//
// # Architecture
//
// The watcher uses a SharedInformer to efficiently watch K8s events with a
// field selector filtering to node-local events only. Captured events are
// stored in a bounded ring buffer and can be queried by time range or pod.
//
//	K8s API → SharedInformer → Filter → RingBuffer → Query API
//
// # Usage
//
//	watcher, _ := events.NewK8sWatcher(clientset, events.WatcherConfig{
//	    NodeName:   "gpu-node-01",
//	    BufferSize: 500,
//	})
//	watcher.Start(ctx)
//	defer watcher.Stop()
//
//	// Query recent events
//	events := watcher.GetEvents(time.Now().Add(-5 * time.Minute))
package events

import (
	"time"
)

// K8sEvent represents a captured Kubernetes event relevant to GPU workloads.
type K8sEvent struct {
	// Timestamp when the event occurred (from event.LastTimestamp or EventTime).
	Timestamp time.Time `json:"timestamp"`

	// Type is the event type: "Normal" or "Warning".
	Type string `json:"type"`

	// Reason is the short, machine-readable reason for the event.
	Reason string `json:"reason"`

	// Message is the human-readable description.
	Message string `json:"message"`

	// PodName is the name of the involved Pod.
	PodName string `json:"pod_name"`

	// PodUID is the UID of the involved Pod.
	PodUID string `json:"pod_uid"`

	// Namespace is the Pod's namespace.
	Namespace string `json:"namespace"`

	// NodeName is the node where the Pod was scheduled.
	NodeName string `json:"node_name"`

	// ContainerID is the container ID if available from the event message.
	ContainerID string `json:"container_id,omitempty"`
}

// GetTimestamp returns the event timestamp.
// Satisfies the blackbox.Timestamped interface for RingBuffer queries.
func (e K8sEvent) GetTimestamp() time.Time {
	return e.Timestamp
}

// Event reasons we capture for GPU workload correlation.
// These are standard Kubernetes event reasons that indicate Pod issues.
const (
	ReasonFailed     = "Failed"
	ReasonOOMKilled  = "OOMKilled"
	ReasonEvicted    = "Evicted"
	ReasonBackOff    = "BackOff"
	ReasonUnhealthy  = "Unhealthy"
	ReasonKilling    = "Killing"
	ReasonPreempting = "Preempting"
)

// relevantReasons is the set of event reasons we capture.
// Warning events are always captured; Normal events only if reason matches.
var relevantReasons = map[string]bool{
	ReasonFailed:     true,
	ReasonOOMKilled:  true,
	ReasonEvicted:    true,
	ReasonBackOff:    true,
	ReasonUnhealthy:  true,
	ReasonKilling:    true,
	ReasonPreempting: true,
}

// IsRelevantReason returns true if the reason should be captured.
// Used for filtering Normal events (Warning events are always captured).
func IsRelevantReason(reason string) bool {
	return relevantReasons[reason]
}

// EventHandler is a callback invoked when a relevant event is captured.
// Handlers are called synchronously in the informer goroutine; long-running
// handlers should spawn their own goroutine.
type EventHandler func(event K8sEvent)

// DefaultBufferSize is the default number of events to retain.
// At ~500 bytes per event, this uses ~250KB of memory.
const DefaultBufferSize = 500

// WatcherConfig holds configuration for the K8sWatcher.
type WatcherConfig struct {
	// NodeName filters events to only those involving Pods on this node.
	// Required.
	NodeName string

	// BufferSize is the ring buffer capacity. Default: 500.
	BufferSize int
}

// Validate checks the configuration for errors.
func (c WatcherConfig) Validate() error {
	if c.NodeName == "" {
		return ErrNodeNameRequired
	}
	return nil
}

// WithDefaults returns a copy of the config with defaults applied.
func (c WatcherConfig) WithDefaults() WatcherConfig {
	if c.BufferSize <= 0 {
		c.BufferSize = DefaultBufferSize
	}
	return c
}
