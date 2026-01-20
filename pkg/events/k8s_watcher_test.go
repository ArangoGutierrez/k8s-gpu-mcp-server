// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestWatcherConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WatcherConfig
		wantErr bool
	}{
		{
			name:    "empty node name",
			cfg:     WatcherConfig{},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: WatcherConfig{
				NodeName:   "gpu-node-01",
				BufferSize: 100,
			},
			wantErr: false,
		},
		{
			name: "zero buffer size uses default",
			cfg: WatcherConfig{
				NodeName:   "gpu-node-01",
				BufferSize: 0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWatcherConfig_WithDefaults(t *testing.T) {
	cfg := WatcherConfig{
		NodeName:   "test-node",
		BufferSize: 0,
	}

	cfg = cfg.WithDefaults()

	if cfg.BufferSize != DefaultBufferSize {
		t.Errorf("WithDefaults() BufferSize = %d, want %d",
			cfg.BufferSize, DefaultBufferSize)
	}
}

func TestNewK8sWatcher(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()

	tests := []struct {
		name    string
		cfg     WatcherConfig
		wantErr bool
	}{
		{
			name:    "invalid config",
			cfg:     WatcherConfig{},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: WatcherConfig{
				NodeName:   "gpu-node-01",
				BufferSize: 100,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := NewK8sWatcher(client, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewK8sWatcher() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && w == nil {
				t.Error("NewK8sWatcher() returned nil without error")
			}
		})
	}
}

func TestK8sWatcher_StartStop(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   "gpu-node-01",
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	// Start watcher
	ctx := context.Background()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if !w.IsRunning() {
		t.Error("IsRunning() = false after Start()")
	}

	// Double start should error
	if err := w.Start(ctx); err != ErrAlreadyStarted {
		t.Errorf("Start() twice error = %v, want ErrAlreadyStarted", err)
	}

	// Stop watcher
	w.Stop()

	if w.IsRunning() {
		t.Error("IsRunning() = true after Stop()")
	}

	// Double stop should be safe
	w.Stop() // Should not panic
}

func TestK8sWatcher_HandleEvent_FiltersPodEvents(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()
	nodeName := "gpu-node-01"

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	// Test with Pod event
	podEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-event",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "test-pod",
			Namespace: "default",
			UID:       "pod-uid-123",
		},
		Type:   corev1.EventTypeWarning,
		Reason: ReasonOOMKilled,
		Source: corev1.EventSource{
			Host: nodeName,
		},
		LastTimestamp: metav1.Now(),
	}

	// Directly call handleEvent
	w.handleEvent(podEvent)

	if w.EventCount() != 1 {
		t.Errorf("EventCount() = %d, want 1 after Pod event", w.EventCount())
	}

	// Test with non-Pod event (should be filtered)
	nodeEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-event",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Node",
			Name: "some-node",
		},
		Type:   corev1.EventTypeWarning,
		Reason: "NodeNotReady",
		Source: corev1.EventSource{
			Host: nodeName,
		},
	}

	w.handleEvent(nodeEvent)

	if w.EventCount() != 1 {
		t.Errorf("EventCount() = %d, want 1 after non-Pod event", w.EventCount())
	}
}

func TestK8sWatcher_HandleEvent_FiltersNormalEvents(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()
	nodeName := "gpu-node-01"

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	// Normal event with relevant reason - should be captured
	relevantEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "relevant-event",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "test-pod",
			Namespace: "default",
		},
		Type:   corev1.EventTypeNormal,
		Reason: ReasonKilling,
		Source: corev1.EventSource{
			Host: nodeName,
		},
		LastTimestamp: metav1.Now(),
	}

	w.handleEvent(relevantEvent)

	if w.EventCount() != 1 {
		t.Errorf("EventCount() = %d, want 1 for relevant Normal event", w.EventCount())
	}

	// Normal event with irrelevant reason - should be filtered
	irrelevantEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "irrelevant-event",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "test-pod-2",
			Namespace: "default",
		},
		Type:   corev1.EventTypeNormal,
		Reason: "Scheduled",
		Source: corev1.EventSource{
			Host: nodeName,
		},
	}

	w.handleEvent(irrelevantEvent)

	if w.EventCount() != 1 {
		t.Errorf("EventCount() = %d, want 1 after irrelevant Normal event", w.EventCount())
	}
}

func TestK8sWatcher_HandleEvent_FiltersOtherNodes(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()
	nodeName := "gpu-node-01"

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	// Event from different node - should be filtered
	otherNodeEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-node-event",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "test-pod",
			Namespace: "default",
		},
		Type:   corev1.EventTypeWarning,
		Reason: ReasonOOMKilled,
		Source: corev1.EventSource{
			Host: "other-node",
		},
		LastTimestamp: metav1.Now(),
	}

	w.handleEvent(otherNodeEvent)

	if w.EventCount() != 0 {
		t.Errorf("EventCount() = %d, want 0 for event from other node", w.EventCount())
	}
}

func TestK8sWatcher_GetEvents(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()
	nodeName := "gpu-node-01"

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	now := time.Now()

	// Add events at different times
	events := []struct {
		name   string
		offset time.Duration
	}{
		{"event-old", -10 * time.Minute},
		{"event-mid", -5 * time.Minute},
		{"event-new", -1 * time.Minute},
	}

	for _, e := range events {
		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e.name,
				Namespace: "default",
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Pod",
				Name:      e.name,
				Namespace: "default",
			},
			Type:   corev1.EventTypeWarning,
			Reason: ReasonFailed,
			Source: corev1.EventSource{
				Host: nodeName,
			},
			LastTimestamp: metav1.NewTime(now.Add(e.offset)),
		}
		w.handleEvent(event)
	}

	// Query events from last 6 minutes
	result := w.GetEvents(now.Add(-6 * time.Minute))

	if len(result) != 2 {
		t.Errorf("GetEvents() returned %d events, want 2", len(result))
	}
}

func TestK8sWatcher_GetEventsForPod(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()
	nodeName := "gpu-node-01"

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	// Add events for different pods
	pods := []struct {
		name      string
		namespace string
	}{
		{"pod-a", "ns-1"},
		{"pod-b", "ns-1"},
		{"pod-a", "ns-2"}, // Same name, different namespace
	}

	for i, p := range pods {
		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      p.name + "-event",
				Namespace: p.namespace,
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Pod",
				Name:      p.name,
				Namespace: p.namespace,
			},
			Type:   corev1.EventTypeWarning,
			Reason: ReasonFailed,
			Source: corev1.EventSource{
				Host: nodeName,
			},
			LastTimestamp: metav1.NewTime(time.Now().Add(time.Duration(i) * time.Second)),
		}
		w.handleEvent(event)
	}

	// Query for pod-a in ns-1
	result := w.GetEventsForPod("pod-a", "ns-1")

	if len(result) != 1 {
		t.Errorf("GetEventsForPod() returned %d events, want 1", len(result))
	}

	if len(result) > 0 && result[0].PodName != "pod-a" {
		t.Errorf("GetEventsForPod() returned pod %s, want pod-a", result[0].PodName)
	}
}

func TestK8sWatcher_RegisterHandler(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()
	nodeName := "gpu-node-01"

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	var mu sync.Mutex
	var receivedEvents []K8sEvent

	// Register handler
	w.RegisterHandler(func(event K8sEvent) {
		mu.Lock()
		defer mu.Unlock()
		receivedEvents = append(receivedEvents, event)
	})

	// Trigger event
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "handler-test-event",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "test-pod",
			Namespace: "default",
		},
		Type:   corev1.EventTypeWarning,
		Reason: ReasonOOMKilled,
		Source: corev1.EventSource{
			Host: nodeName,
		},
		LastTimestamp: metav1.Now(),
	}

	w.handleEvent(event)

	mu.Lock()
	count := len(receivedEvents)
	mu.Unlock()

	if count != 1 {
		t.Errorf("Handler received %d events, want 1", count)
	}
}

func TestK8sWatcher_ConvertEvent_Timestamps(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()
	nodeName := "gpu-node-01"

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	now := time.Now()
	eventTime := metav1.NewMicroTime(now.Add(-1 * time.Hour))
	lastTime := metav1.NewTime(now.Add(-30 * time.Minute))
	firstTime := metav1.NewTime(now.Add(-2 * time.Hour))

	tests := []struct {
		name     string
		event    *corev1.Event
		wantTime time.Time
	}{
		{
			name: "prefer EventTime",
			event: &corev1.Event{
				EventTime:      eventTime,
				LastTimestamp:  lastTime,
				FirstTimestamp: firstTime,
				InvolvedObject: corev1.ObjectReference{Kind: "Pod"},
			},
			wantTime: eventTime.Time,
		},
		{
			name: "fallback to LastTimestamp",
			event: &corev1.Event{
				LastTimestamp:  lastTime,
				FirstTimestamp: firstTime,
				InvolvedObject: corev1.ObjectReference{Kind: "Pod"},
			},
			wantTime: lastTime.Time,
		},
		{
			name: "fallback to FirstTimestamp",
			event: &corev1.Event{
				FirstTimestamp: firstTime,
				InvolvedObject: corev1.ObjectReference{Kind: "Pod"},
			},
			wantTime: firstTime.Time,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := w.convertEvent(tt.event)
			if !result.Timestamp.Equal(tt.wantTime) {
				t.Errorf("convertEvent() timestamp = %v, want %v",
					result.Timestamp, tt.wantTime)
			}
		})
	}
}

func TestIsRelevantReason(t *testing.T) {
	tests := []struct {
		reason string
		want   bool
	}{
		{ReasonFailed, true},
		{ReasonOOMKilled, true},
		{ReasonEvicted, true},
		{ReasonBackOff, true},
		{ReasonUnhealthy, true},
		{ReasonKilling, true},
		{ReasonPreempting, true},
		{"Scheduled", false},
		{"Pulled", false},
		{"Started", false},
		{"Created", false},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			if got := IsRelevantReason(tt.reason); got != tt.want {
				t.Errorf("IsRelevantReason(%q) = %v, want %v",
					tt.reason, got, tt.want)
			}
		})
	}
}

func TestEventBuffer_Basic(t *testing.T) {
	buf, err := NewEventBuffer(10)
	if err != nil {
		t.Fatalf("NewEventBuffer() error = %v", err)
	}

	if buf.Size() != 0 {
		t.Errorf("Size() = %d, want 0", buf.Size())
	}

	if buf.Capacity() != 10 {
		t.Errorf("Capacity() = %d, want 10", buf.Capacity())
	}

	// Add events
	now := time.Now()
	for i := 0; i < 5; i++ {
		buf.Add(K8sEvent{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			PodName:   "test-pod",
			Namespace: "default",
			Reason:    ReasonFailed,
		})
	}

	if buf.Size() != 5 {
		t.Errorf("Size() = %d after 5 adds, want 5", buf.Size())
	}

	// Get all
	all := buf.GetAll()
	if len(all) != 5 {
		t.Errorf("GetAll() returned %d events, want 5", len(all))
	}

	// Latest
	latest, ok := buf.Latest()
	if !ok {
		t.Error("Latest() returned false")
	}
	if !latest.Timestamp.Equal(now.Add(4 * time.Second)) {
		t.Error("Latest() returned wrong event")
	}

	// Clear
	buf.Clear()
	if buf.Size() != 0 {
		t.Errorf("Size() = %d after Clear(), want 0", buf.Size())
	}
}

func TestEventBuffer_WrapAround(t *testing.T) {
	buf, err := NewEventBuffer(3)
	if err != nil {
		t.Fatalf("NewEventBuffer() error = %v", err)
	}

	now := time.Now()

	// Add 5 events to 3-capacity buffer
	for i := 0; i < 5; i++ {
		buf.Add(K8sEvent{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			PodName:   "pod-" + string(rune('a'+i)),
		})
	}

	if buf.Size() != 3 {
		t.Errorf("Size() = %d after wraparound, want 3", buf.Size())
	}

	// Should have events 2, 3, 4 (oldest two evicted)
	all := buf.GetAll()
	if len(all) != 3 {
		t.Fatalf("GetAll() returned %d events, want 3", len(all))
	}

	// Check chronological order
	if all[0].PodName != "pod-c" {
		t.Errorf("First event is %s, want pod-c", all[0].PodName)
	}
	if all[2].PodName != "pod-e" {
		t.Errorf("Last event is %s, want pod-e", all[2].PodName)
	}
}

func TestEventBuffer_GetByReason(t *testing.T) {
	buf, err := NewEventBuffer(10)
	if err != nil {
		t.Fatalf("NewEventBuffer() error = %v", err)
	}

	now := time.Now()
	reasons := []string{ReasonOOMKilled, ReasonFailed, ReasonOOMKilled, ReasonBackOff}

	for i, r := range reasons {
		buf.Add(K8sEvent{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Reason:    r,
		})
	}

	oomEvents := buf.GetByReason(ReasonOOMKilled)
	if len(oomEvents) != 2 {
		t.Errorf("GetByReason(OOMKilled) returned %d, want 2", len(oomEvents))
	}
}

// TestK8sWatcher_Integration tests the watcher with a fake clientset
// using reactor to simulate event creation.
func TestK8sWatcher_Integration(t *testing.T) {
	nodeName := "gpu-node-01"

	// Create fake clientset with reactor
	//nolint:staticcheck // NewSimpleClientset used for testing
	client := fake.NewSimpleClientset()

	// Prepend a reactor to return a fake watcher
	client.PrependWatchReactor("events", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		// Return a fake watcher
		fakeWatch := watch.NewFake()
		return true, fakeWatch, nil
	})

	w, err := NewK8sWatcher(client, WatcherConfig{
		NodeName:   nodeName,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("NewK8sWatcher() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer w.Stop()

	// Give informer time to start
	time.Sleep(100 * time.Millisecond)

	if !w.IsRunning() {
		t.Error("watcher should be running")
	}
}

// BenchmarkEventBuffer_Add benchmarks adding events to the buffer.
func BenchmarkEventBuffer_Add(b *testing.B) {
	buf, _ := NewEventBuffer(1000)
	event := K8sEvent{
		Timestamp: time.Now(),
		Type:      corev1.EventTypeWarning,
		Reason:    ReasonOOMKilled,
		Message:   "Container was OOMKilled",
		PodName:   "benchmark-pod",
		PodUID:    "uid-123",
		Namespace: "default",
		NodeName:  "gpu-node-01",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(event)
	}
}

// BenchmarkEventBuffer_Query benchmarks querying events by time.
func BenchmarkEventBuffer_Query(b *testing.B) {
	buf, _ := NewEventBuffer(1000)
	now := time.Now()

	// Fill buffer
	for i := 0; i < 1000; i++ {
		buf.Add(K8sEvent{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			PodName:   "benchmark-pod",
		})
	}

	since := now.Add(500 * time.Second)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buf.GetSince(since)
	}
}
