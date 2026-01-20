// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// K8sWatcher monitors Kubernetes events for GPU workload correlation.
// It uses a SharedInformer to efficiently watch node-local events and
// stores relevant events in a ring buffer for querying.
type K8sWatcher struct {
	// Configuration
	client   kubernetes.Interface
	config   WatcherConfig
	logger   *slog.Logger
	nodeName string

	// State
	buffer   *EventBuffer
	handlers []EventHandler
	mu       sync.RWMutex // Protects handlers slice

	// Informer
	informer cache.SharedIndexInformer

	// Lifecycle
	running atomic.Bool
	stopCh  chan struct{}
}

// WatcherOption configures a K8sWatcher.
type WatcherOption func(*K8sWatcher)

// WithWatcherLogger sets the logger for the watcher.
func WithWatcherLogger(logger *slog.Logger) WatcherOption {
	return func(w *K8sWatcher) {
		if logger != nil {
			w.logger = logger
		}
	}
}

// NewK8sWatcher creates a new Kubernetes event watcher.
// The watcher must be started with Start() before it begins capturing events.
func NewK8sWatcher(
	client kubernetes.Interface,
	cfg WatcherConfig,
	opts ...WatcherOption,
) (*K8sWatcher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	cfg = cfg.WithDefaults()

	buffer, err := NewEventBuffer(cfg.BufferSize)
	if err != nil {
		return nil, fmt.Errorf("create buffer: %w", err)
	}

	w := &K8sWatcher{
		client:   client,
		config:   cfg,
		nodeName: cfg.NodeName,
		buffer:   buffer,
		logger:   slog.Default(),
	}

	for _, opt := range opts {
		opt(w)
	}

	return w, nil
}

// Start begins watching Kubernetes events.
// Returns ErrAlreadyStarted if the watcher is already running.
func (w *K8sWatcher) Start(ctx context.Context) error {
	if !w.running.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}

	// Create ListWatch for Kubernetes events.
	// Server-side node filtering is not supported for events; node-local
	// filtering is applied in handleEvent via extractNodeName.
	// Use context.Background() since informer lifecycle is managed via stopCh,
	// not the context passed to Start().
	listWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return w.client.CoreV1().Events("").List(context.Background(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return w.client.CoreV1().Events("").Watch(context.Background(), options)
		},
	}

	// Create SharedIndexInformer
	w.informer = cache.NewSharedIndexInformer(
		listWatch,
		&corev1.Event{},
		0, // No resync period - we handle events in real-time
		cache.Indexers{},
	)

	// Add event handlers
	_, err := w.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			w.handleEvent(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			w.handleEvent(newObj)
		},
		// We don't care about deletes - events are ephemeral anyway
	})
	if err != nil {
		w.running.Store(false)
		return fmt.Errorf("add event handler: %w", err)
	}

	w.stopCh = make(chan struct{})

	// Start informer in background
	go w.informer.Run(w.stopCh)

	// Wait for cache sync
	go func() {
		if !cache.WaitForCacheSync(w.stopCh, w.informer.HasSynced) {
			w.logger.Error("failed to sync informer cache")
		} else {
			w.logger.Info("k8s event watcher started",
				"node", w.nodeName,
				"bufferSize", w.config.BufferSize,
			)
		}
	}()

	return nil
}

// Stop gracefully shuts down the watcher.
// Safe to call multiple times or if not started.
func (w *K8sWatcher) Stop() {
	if !w.running.CompareAndSwap(true, false) {
		return
	}

	close(w.stopCh)
	w.logger.Info("k8s event watcher stopped")
}

// IsRunning returns true if the watcher is actively watching events.
func (w *K8sWatcher) IsRunning() bool {
	return w.running.Load()
}

// GetEvents returns events newer than the given timestamp.
// Events are returned in chronological order (oldest first).
func (w *K8sWatcher) GetEvents(since time.Time) []K8sEvent {
	return w.buffer.GetSince(since)
}

// GetEventsForPod returns all captured events for the specified pod.
// Events are returned in chronological order (oldest first).
func (w *K8sWatcher) GetEventsForPod(podName, namespace string) []K8sEvent {
	return w.buffer.GetForPod(podName, namespace)
}

// GetEventsByReason returns all events with the specified reason.
// Events are returned in chronological order (oldest first).
func (w *K8sWatcher) GetEventsByReason(reason string) []K8sEvent {
	return w.buffer.GetByReason(reason)
}

// GetAllEvents returns all captured events.
// Events are returned in chronological order (oldest first).
func (w *K8sWatcher) GetAllEvents() []K8sEvent {
	return w.buffer.GetAll()
}

// EventCount returns the current number of events in the buffer.
func (w *K8sWatcher) EventCount() int {
	return w.buffer.Size()
}

// RegisterHandler adds a callback for real-time event notifications.
// Handlers are called synchronously in the informer goroutine.
// For long-running operations, handlers should spawn their own goroutine.
func (w *K8sWatcher) RegisterHandler(handler EventHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers = append(w.handlers, handler)
}

// handleEvent processes an incoming Kubernetes event.
func (w *K8sWatcher) handleEvent(obj interface{}) {
	event, ok := obj.(*corev1.Event)
	if !ok {
		w.logger.Debug("received non-event object", "type", fmt.Sprintf("%T", obj))
		return
	}

	// Filter: only Pod events
	if event.InvolvedObject.Kind != "Pod" {
		return
	}

	// Filter: only events on our node
	// The node name can be in different places depending on the event
	nodeName := w.extractNodeName(event)
	if nodeName != "" && nodeName != w.nodeName {
		return
	}

	// Filter: Warning events OR relevant Normal events
	if event.Type != corev1.EventTypeWarning && !IsRelevantReason(event.Reason) {
		return
	}

	// Convert to our event type
	k8sEvent := w.convertEvent(event)

	// Add to buffer
	w.buffer.Add(k8sEvent)

	w.logger.Debug("captured event",
		"type", k8sEvent.Type,
		"reason", k8sEvent.Reason,
		"pod", k8sEvent.PodName,
		"namespace", k8sEvent.Namespace,
	)

	// Notify handlers
	w.mu.RLock()
	handlers := w.handlers
	w.mu.RUnlock()

	for _, handler := range handlers {
		handler(k8sEvent)
	}
}

// extractNodeName attempts to extract the node name from an event.
// Returns empty string if node cannot be determined.
func (w *K8sWatcher) extractNodeName(event *corev1.Event) string {
	// Check source component for node name (common for kubelet events)
	if event.Source.Host != "" {
		return event.Source.Host
	}

	// Check reporting instance (newer events API)
	if event.ReportingInstance != "" {
		return event.ReportingInstance
	}

	// For events from the scheduler or controller-manager,
	// we'd need to look up the Pod to find its node.
	// For now, we accept these events as potentially relevant.
	return ""
}

// convertEvent converts a Kubernetes Event to our K8sEvent type.
func (w *K8sWatcher) convertEvent(event *corev1.Event) K8sEvent {
	// Determine the best timestamp
	// Prefer EventTime (newer API), fall back to LastTimestamp, then FirstTimestamp
	var timestamp time.Time
	if !event.EventTime.IsZero() {
		timestamp = event.EventTime.Time
	} else if !event.LastTimestamp.IsZero() {
		timestamp = event.LastTimestamp.Time
	} else if !event.FirstTimestamp.IsZero() {
		timestamp = event.FirstTimestamp.Time
	} else {
		timestamp = time.Now()
	}

	return K8sEvent{
		Timestamp: timestamp,
		Type:      event.Type,
		Reason:    event.Reason,
		Message:   event.Message,
		PodName:   event.InvolvedObject.Name,
		PodUID:    string(event.InvolvedObject.UID),
		Namespace: event.InvolvedObject.Namespace,
		NodeName:  w.extractNodeName(event),
	}
}
