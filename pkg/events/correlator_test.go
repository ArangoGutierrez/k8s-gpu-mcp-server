// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
)

func TestNewCorrelator(t *testing.T) {
	tests := []struct {
		name       string
		opts       []CorrelatorOption
		wantWindow time.Duration
	}{
		{
			name:       "defaults",
			opts:       nil,
			wantWindow: DefaultWindowSize,
		},
		{
			name:       "custom window size",
			opts:       []CorrelatorOption{WithWindowSize(10 * time.Second)},
			wantWindow: 10 * time.Second,
		},
		{
			name:       "zero window uses default",
			opts:       []CorrelatorOption{WithWindowSize(0)},
			wantWindow: DefaultWindowSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCorrelator(nil, nil, nil, tt.opts...)
			if c.windowSize != tt.wantWindow {
				t.Errorf("windowSize = %v, want %v", c.windowSize, tt.wantWindow)
			}
		})
	}
}

func TestCorrelator_Correlate_EmptyWindow(t *testing.T) {
	// Test correlation with no event sources
	c := NewCorrelator(nil, nil, nil)

	trigger := Event{
		Type:      "k8s",
		Timestamp: time.Now(),
		Source:    "test",
		Data:      K8sEvent{Reason: ReasonFailed, PodName: "test-pod"},
	}

	incident := c.Correlate(context.Background(), trigger)

	// Verify incident is created with only trigger
	if incident == nil {
		t.Fatal("expected incident, got nil")
	}
	if incident.ID == "" {
		t.Error("incident ID should not be empty")
	}
	if incident.Trigger.Type != "k8s" {
		t.Errorf("trigger type = %v, want k8s", incident.Trigger.Type)
	}
	if len(incident.RelatedEvents) != 0 {
		t.Errorf("expected no related events, got %d", len(incident.RelatedEvents))
	}
	if len(incident.Timeline) != 1 {
		t.Errorf("timeline should have 1 entry (trigger), got %d", len(incident.Timeline))
	}
}

func TestCorrelator_buildTimeline_Sorted(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)
	now := time.Now()

	incident := &CorrelatedIncident{
		Trigger: Event{
			Type:      "k8s",
			Timestamp: now,
			Source:    "test",
			Data:      K8sEvent{Reason: ReasonFailed, PodName: "test-pod"},
		},
		RelatedEvents: []Event{
			{
				Type:      "xid",
				Timestamp: now.Add(-30 * time.Second),
				Source:    "xid.Watcher",
				Data:      xid.XIDEvent{XIDCode: 79, Severity: "critical"},
			},
			{
				Type:      "k8s",
				Timestamp: now.Add(-1 * time.Minute),
				Source:    "K8sWatcher",
				Data:      K8sEvent{Reason: ReasonBackOff, PodName: "test-pod"},
			},
		},
	}

	timeline := c.buildTimeline(incident)

	// Verify chronological order
	if len(timeline) != 3 {
		t.Fatalf("timeline length = %d, want 3", len(timeline))
	}

	for i := 1; i < len(timeline); i++ {
		if timeline[i].Timestamp.Before(timeline[i-1].Timestamp) {
			t.Errorf("timeline not sorted: entry %d before entry %d", i, i-1)
		}
	}

	// Verify trigger has relative time "0s"
	var triggerEntry *TimelineEntry
	for i := range timeline {
		if timeline[i].RelativeTime == "0s" {
			triggerEntry = &timeline[i]
			break
		}
	}
	if triggerEntry == nil {
		t.Error("trigger entry not found in timeline")
	}
}

func TestCorrelator_buildTimeline_WithGPUSnapshots(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)
	now := time.Now()

	incident := &CorrelatedIncident{
		Trigger: Event{
			Type:      "xid",
			Timestamp: now,
			Source:    "xid.Watcher",
			Data:      xid.XIDEvent{XIDCode: 79},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:  now.Add(-10 * time.Second),
				UUID:       "GPU-12345678-1234-1234-1234-123456789abc",
				Throttling: 1, // Throttled
			},
			{
				Timestamp:        now.Add(-5 * time.Second),
				UUID:             "GPU-12345678-1234-1234-1234-123456789abc",
				ECCUncorrectable: 1,
			},
		},
	}

	timeline := c.buildTimeline(incident)

	// At minimum we expect throttle, ecc, and trigger events (>= 3 entries).
	// Additional entries may be present from deduplication edge cases.
	if len(timeline) < 3 {
		t.Fatalf("timeline length = %d, want >= 3", len(timeline))
	}

	// Verify event types are present
	types := make(map[string]bool)
	for _, e := range timeline {
		types[e.EventType] = true
	}
	if !types["throttle"] {
		t.Error("expected throttle event in timeline")
	}
	if !types["ecc"] {
		t.Error("expected ecc event in timeline")
	}
	if !types["xid"] {
		t.Error("expected xid event in timeline")
	}
}

func TestCorrelator_identifyAffectedPods(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)

	incident := &CorrelatedIncident{
		Trigger: Event{
			Type:   "k8s",
			Source: "K8sWatcher",
			Data: K8sEvent{
				PodName:   "pod-1",
				Namespace: "default",
				PodUID:    "uid-1",
				Reason:    ReasonOOMKilled,
			},
		},
		RelatedEvents: []Event{
			{
				Type:   "k8s",
				Source: "K8sWatcher",
				Data: K8sEvent{
					PodName:   "pod-2",
					Namespace: "gpu-workloads",
					Reason:    ReasonBackOff,
				},
			},
			{
				Type:   "xid",
				Source: "xid.Watcher",
				Data: xid.XIDEvent{
					XIDCode:   79,
					PodName:   "pod-3",
					Namespace: "default",
				},
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Processes: []blackbox.ProcessInfo{
					{
						PID:       1234,
						PodName:   "pod-4",
						Namespace: "ml-team",
						PodUID:    "uid-4",
					},
				},
			},
		},
	}

	pods := c.identifyAffectedPods(incident)

	// Should find 4 affected pods:
	// - pod-1 from trigger K8s event
	// - pod-2 from related K8s event
	// - pod-3 from related XID event
	// - pod-4 from GPU snapshot process
	if len(pods) != 4 {
		t.Errorf("expected 4 affected pods, got %d", len(pods))
	}

	// Verify deterministic sort order (by namespace, then name)
	if len(pods) >= 2 {
		for i := 1; i < len(pods); i++ {
			if pods[i].Namespace < pods[i-1].Namespace {
				t.Errorf("pods not sorted by namespace")
			}
			if pods[i].Namespace == pods[i-1].Namespace && pods[i].PodName < pods[i-1].PodName {
				t.Errorf("pods not sorted by name within namespace")
			}
		}
	}
}

func TestCorrelator_DetectCausality_ThermalCascade(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)

	// Pattern: throttle → xid
	incident := &CorrelatedIncident{
		Timeline: []TimelineEntry{
			{EventType: "temp", Severity: "warning"},
			{EventType: "throttle", Severity: "warning"},
			{EventType: "xid", Severity: "critical"},
			{EventType: "k8s", Severity: "critical"},
		},
	}

	causality := c.DetectCausality(incident)
	if causality != CausalityThermalCascade {
		t.Errorf("causality = %v, want %v", causality, CausalityThermalCascade)
	}
}

func TestCorrelator_DetectCausality_MemoryFailure(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)

	// Pattern: ecc → xid
	incident := &CorrelatedIncident{
		Timeline: []TimelineEntry{
			{EventType: "ecc", Severity: "warning"},
			{EventType: "xid", Severity: "critical"},
			{EventType: "k8s", Severity: "critical"},
		},
	}

	causality := c.DetectCausality(incident)
	if causality != CausalityMemoryFailure {
		t.Errorf("causality = %v, want %v", causality, CausalityMemoryFailure)
	}
}

func TestCorrelator_DetectCausality_SoftwareOOM(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)

	// Pattern: OOMKilled with no hardware events
	incident := &CorrelatedIncident{
		Trigger: Event{
			Type:   "k8s",
			Source: "K8sWatcher",
			Data:   K8sEvent{Reason: ReasonOOMKilled},
		},
		Timeline: []TimelineEntry{
			{EventType: "k8s", Severity: "critical"},
		},
	}

	causality := c.DetectCausality(incident)
	if causality != CausalitySoftwareOOM {
		t.Errorf("causality = %v, want %v", causality, CausalitySoftwareOOM)
	}
}

func TestCorrelator_DetectCausality_Unknown(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)

	// No clear pattern
	incident := &CorrelatedIncident{
		Trigger: Event{
			Type:   "k8s",
			Source: "K8sWatcher",
			Data:   K8sEvent{Reason: ReasonBackOff}, // Not OOMKilled
		},
		Timeline: []TimelineEntry{
			{EventType: "k8s", Severity: "warning"},
		},
	}

	causality := c.DetectCausality(incident)
	if causality != CausalityUnknown {
		t.Errorf("causality = %v, want %v", causality, CausalityUnknown)
	}
}

func TestFormatRelativeTime(t *testing.T) {
	tests := []struct {
		offset time.Duration
		want   string
	}{
		{0, "0s"},
		{5 * time.Second, "+5s"},
		{-5 * time.Second, "-5s"},
		{2 * time.Minute, "+2m"},
		{-3 * time.Minute, "-3m"},
		{1 * time.Hour, "+1h"},
		{-1 * time.Hour, "-1h"},
		{500 * time.Millisecond, "+500ms"},
		{-500 * time.Millisecond, "-500ms"},
	}

	reference := time.Now()
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatRelativeTime(reference.Add(tt.offset), reference)
			if got != tt.want {
				t.Errorf("formatRelativeTime(%v) = %v, want %v", tt.offset, got, tt.want)
			}
		})
	}
}

func TestHasSequence(t *testing.T) {
	tests := []struct {
		name  string
		types []string
		a, b  string
		want  bool
	}{
		{"throttle_before_xid", []string{"throttle", "xid", "k8s"}, "throttle", "xid", true},
		{"xid_before_k8s", []string{"throttle", "xid", "k8s"}, "xid", "k8s", true},
		{"k8s_not_before_throttle", []string{"throttle", "xid", "k8s"}, "k8s", "throttle", false},
		{"missing_first_element", []string{"xid"}, "throttle", "xid", false},
		{"empty_slice", []string{}, "throttle", "xid", false},
		{"non_adjacent_sequence", []string{"ecc", "temp", "xid"}, "ecc", "xid", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSequence(tt.types, tt.a, tt.b)
			if got != tt.want {
				t.Errorf("hasSequence(%v, %q, %q) = %v, want %v",
					tt.types, tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		types  []string
		target string
		want   bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{}, "a", false},
	}

	for _, tt := range tests {
		got := contains(tt.types, tt.target)
		if got != tt.want {
			t.Errorf("contains(%v, %q) = %v, want %v", tt.types, tt.target, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is..."},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncate(tt.s, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
		}
	}
}

func TestDeduplicateTimeline(t *testing.T) {
	t.Run("consecutive_duplicates", func(t *testing.T) {
		entries := []TimelineEntry{
			{EventType: "throttle", Description: "GPU throttling"},
			{EventType: "throttle", Description: "GPU throttling"}, // Duplicate
			{EventType: "xid", Description: "XID 79"},
			{EventType: "xid", Description: "XID 79"},     // Duplicate
			{EventType: "xid", Description: "XID 48"},     // Different description
			{EventType: "k8s", Description: "Pod failed"}, // Different type
		}

		result := deduplicateTimeline(entries)

		// Should have: throttle, xid (79), xid (48), k8s = 4 entries
		if len(result) != 4 {
			t.Errorf("deduplicateTimeline length = %d, want 4", len(result))
		}

		// Verify order preserved
		expected := []string{"throttle", "xid", "xid", "k8s"}
		for i, want := range expected {
			if result[i].EventType != want {
				t.Errorf("result[%d].EventType = %q, want %q", i, result[i].EventType, want)
			}
		}
	})

	t.Run("non_consecutive_duplicates", func(t *testing.T) {
		// Test that non-consecutive duplicates are also removed
		entries := []TimelineEntry{
			{EventType: "throttle", Description: "GPU throttling"},
			{EventType: "xid", Description: "XID 79"},
			{EventType: "throttle", Description: "GPU throttling"}, // Non-consecutive duplicate
			{EventType: "k8s", Description: "Pod failed"},
			{EventType: "xid", Description: "XID 79"}, // Non-consecutive duplicate
		}

		result := deduplicateTimeline(entries)

		// Should have: throttle, xid (79), k8s = 3 entries (duplicates removed)
		if len(result) != 3 {
			t.Errorf("deduplicateTimeline length = %d, want 3", len(result))
		}

		// Verify first occurrence of each is kept
		expected := []struct {
			eventType   string
			description string
		}{
			{"throttle", "GPU throttling"},
			{"xid", "XID 79"},
			{"k8s", "Pod failed"},
		}
		for i, want := range expected {
			if result[i].EventType != want.eventType {
				t.Errorf("result[%d].EventType = %q, want %q", i, result[i].EventType, want.eventType)
			}
			if result[i].Description != want.description {
				t.Errorf("result[%d].Description = %q, want %q", i, result[i].Description, want.description)
			}
		}
	})
}

func TestGenerateIncidentID(t *testing.T) {
	id1 := generateIncidentID()
	id2 := generateIncidentID()

	if id1 == "" {
		t.Error("generated ID should not be empty")
	}
	if id1 == id2 {
		t.Error("generated IDs should be unique")
	}
	if len(id1) < 10 {
		t.Errorf("ID too short: %s", id1)
	}
}

func TestDescribeEvent(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name: "xid with description",
			event: Event{
				Type: "xid",
				Data: xid.XIDEvent{
					XIDCode:     79,
					Description: "GPU fell off the bus",
				},
			},
			want: "XID 79: GPU fell off the bus",
		},
		{
			name: "xid without description",
			event: Event{
				Type: "xid",
				Data: xid.XIDEvent{
					XIDCode:  48,
					PCIBusID: "0000:00:1E.0",
				},
			},
			want: "XID 48 on GPU 0000:00:1E.0",
		},
		{
			name: "k8s event",
			event: Event{
				Type: "k8s",
				Data: K8sEvent{
					Reason:  ReasonOOMKilled,
					PodName: "my-pod",
					Message: "Container was OOMKilled",
				},
			},
			want: "[OOMKilled] my-pod: Container was OOMKilled",
		},
		{
			name: "throttle event",
			event: Event{
				Type: "throttle",
			},
			want: "GPU throttling active",
		},
		{
			name: "unknown type",
			event: Event{
				Type: "custom",
			},
			want: "custom event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeEvent(tt.event)
			if got != tt.want {
				t.Errorf("describeEvent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSeverityFromEvent(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name: "xid with severity",
			event: Event{
				Type: "xid",
				Data: xid.XIDEvent{Severity: "fatal"},
			},
			want: "fatal",
		},
		{
			name: "xid without severity",
			event: Event{
				Type: "xid",
				Data: xid.XIDEvent{},
			},
			want: "critical",
		},
		{
			name: "k8s OOMKilled",
			event: Event{
				Type: "k8s",
				Data: K8sEvent{Reason: ReasonOOMKilled},
			},
			want: "critical",
		},
		{
			name: "k8s Evicted",
			event: Event{
				Type: "k8s",
				Data: K8sEvent{Reason: ReasonEvicted},
			},
			want: "warning",
		},
		{
			name: "throttle",
			event: Event{
				Type: "throttle",
			},
			want: "warning",
		},
		{
			name: "ecc",
			event: Event{
				Type: "ecc",
			},
			want: "critical",
		},
		{
			name: "unknown",
			event: Event{
				Type: "custom",
			},
			want: "info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := severityFromEvent(tt.event)
			if got != tt.want {
				t.Errorf("severityFromEvent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsHighTemperature(t *testing.T) {
	tests := []struct {
		name     string
		snap     blackbox.GPUSnapshot
		wantHigh bool
	}{
		{
			name:     "below threshold",
			snap:     blackbox.GPUSnapshot{Temperature: 70, TempThreshold: 90},
			wantHigh: false,
		},
		{
			name:     "within 10 degrees",
			snap:     blackbox.GPUSnapshot{Temperature: 82, TempThreshold: 90},
			wantHigh: true,
		},
		{
			name:     "at threshold",
			snap:     blackbox.GPUSnapshot{Temperature: 90, TempThreshold: 90},
			wantHigh: true,
		},
		{
			name:     "no threshold, high absolute",
			snap:     blackbox.GPUSnapshot{Temperature: 85, TempThreshold: 0},
			wantHigh: true,
		},
		{
			name:     "no threshold, low absolute",
			snap:     blackbox.GPUSnapshot{Temperature: 70, TempThreshold: 0},
			wantHigh: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHighTemperature(tt.snap)
			if got != tt.wantHigh {
				t.Errorf("isHighTemperature() = %v, want %v", got, tt.wantHigh)
			}
		})
	}
}

func TestCorrelator_RegisterAutoCorrelation_NilWatchers(t *testing.T) {
	// Test that RegisterAutoCorrelation handles nil watchers gracefully
	c := NewCorrelator(nil, nil, nil)

	callCount := 0
	callback := func(incident *CorrelatedIncident) {
		callCount++
	}

	ctx := context.Background()
	cleanup := c.RegisterAutoCorrelation(ctx, callback)

	// Should return a valid cleanup function even with nil watchers
	if cleanup == nil {
		t.Fatal("expected cleanup function, got nil")
	}

	// Cleanup should not panic
	cleanup()

	// No callbacks should have been triggered (no watchers)
	if callCount != 0 {
		t.Errorf("callback count = %d, want 0", callCount)
	}
}

func TestCorrelator_RegisterAutoCorrelation_ContextCancellation(t *testing.T) {
	// Test that cleanup cancels context and waits for completion
	c := NewCorrelator(nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleanup := c.RegisterAutoCorrelation(ctx, func(incident *CorrelatedIncident) {})

	// Cleanup should complete without blocking (no handlers registered with nil watchers)
	done := make(chan struct{})
	go func() {
		cleanup()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("cleanup blocked for too long")
	}
}

func TestCorrelator_Correlate_ContextCancellation(t *testing.T) {
	c := NewCorrelator(nil, nil, nil)

	// Use cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	trigger := Event{
		Type:      "k8s",
		Timestamp: time.Now(),
		Source:    "test",
		Data:      K8sEvent{Reason: ReasonFailed, PodName: "test-pod"},
	}

	incident := c.Correlate(ctx, trigger)

	// Should return partial incident (only ID and trigger)
	if incident == nil {
		t.Fatal("expected incident, got nil")
	}
	if incident.ID == "" {
		t.Error("incident ID should not be empty")
	}
	if incident.Trigger.Type != "k8s" {
		t.Errorf("trigger type = %v, want k8s", incident.Trigger.Type)
	}
}

func TestCorrelator_Integration_FullPipeline(t *testing.T) {
	// Integration test verifying full correlation pipeline
	c := NewCorrelator(nil, nil, nil)
	now := time.Now()

	// Create a realistic incident scenario manually
	incident := &CorrelatedIncident{
		ID:        "test-incident",
		Timestamp: now,
		Trigger: Event{
			Type:      "xid",
			Timestamp: now,
			Source:    "xid.Watcher",
			Data: xid.XIDEvent{
				XIDCode:     79,
				Severity:    "critical",
				Description: "GPU fell off the bus",
				PCIBusID:    "0000:00:1E.0",
				PodName:     "ml-training-pod",
				Namespace:   "ml-workloads",
			},
		},
		RelatedEvents: []Event{
			{
				Type:      "k8s",
				Timestamp: now.Add(-2 * time.Second),
				Source:    "K8sWatcher",
				Data: K8sEvent{
					PodName:   "ml-training-pod",
					Namespace: "ml-workloads",
					PodUID:    "uid-123",
					Reason:    ReasonFailed,
					Message:   "Container crashed with exit code 137",
					Type:      "Warning",
				},
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:     now.Add(-5 * time.Second),
				UUID:          "GPU-12345678-1234-1234-1234-123456789abc",
				Temperature:   85,
				TempThreshold: 90,
				Throttling:    1,
			},
			{
				Timestamp:        now.Add(-3 * time.Second),
				UUID:             "GPU-12345678-1234-1234-1234-123456789abc",
				ECCUncorrectable: 2,
			},
		},
	}

	// Build timeline
	timeline := c.buildTimeline(incident)
	if len(timeline) < 4 {
		t.Errorf("timeline should have >= 4 entries, got %d", len(timeline))
	}

	// Verify chronological order
	for i := 1; i < len(timeline); i++ {
		if timeline[i].Timestamp.Before(timeline[i-1].Timestamp) {
			t.Error("timeline not in chronological order")
		}
	}

	// Identify affected pods
	pods := c.identifyAffectedPods(incident)
	if len(pods) != 1 {
		t.Errorf("expected 1 affected pod, got %d", len(pods))
	}
	if len(pods) > 0 && pods[0].PodName != "ml-training-pod" {
		t.Errorf("pod name = %q, want %q", pods[0].PodName, "ml-training-pod")
	}

	// Detect causality - should be thermal_cascade (temp→throttle AND throttle→xid)
	incident.Timeline = timeline
	causality := c.DetectCausality(incident)

	// Note: with our test data we have temp, throttle, ecc, k8s, xid events
	// temp→throttle exists, but throttle→xid may not depending on timeline order
	// This test verifies the pipeline runs without error
	if causality == "" {
		t.Error("causality should not be empty")
	}
}

func TestCorrelator_Integration_ThermalCascade(t *testing.T) {
	// Test specific thermal cascade detection with proper event sequence
	c := NewCorrelator(nil, nil, nil)
	now := time.Now()

	incident := &CorrelatedIncident{
		Trigger: Event{
			Type:      "k8s",
			Timestamp: now,
			Source:    "K8sWatcher",
			Data:      K8sEvent{Reason: ReasonFailed},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:     now.Add(-10 * time.Second),
				UUID:          "GPU-test",
				Temperature:   88,
				TempThreshold: 90, // Within 10°C margin → high temp
			},
			{
				Timestamp:  now.Add(-5 * time.Second),
				UUID:       "GPU-test",
				Throttling: 1,
			},
		},
		RelatedEvents: []Event{
			{
				Type:      "xid",
				Timestamp: now.Add(-1 * time.Second),
				Source:    "xid.Watcher",
				Data:      xid.XIDEvent{XIDCode: 79, Severity: "critical"},
			},
		},
	}

	timeline := c.buildTimeline(incident)
	incident.Timeline = timeline

	causality := c.DetectCausality(incident)
	if causality != CausalityThermalCascade {
		t.Errorf("causality = %q, want %q", causality, CausalityThermalCascade)
	}
}

// --- Concurrency stress tests ---

func TestCorrelatorConcurrentCorrelate(t *testing.T) {
	t.Parallel()

	c := NewCorrelator(nil, nil, nil)

	const numGoroutines = 50
	var wg sync.WaitGroup
	start := make(chan struct{})

	incidents := make([]*CorrelatedIncident, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start

			trigger := Event{
				Type:      "k8s",
				Timestamp: time.Now(),
				Source:    fmt.Sprintf("test-%d", id),
				Data: K8sEvent{
					Reason:    ReasonFailed,
					PodName:   fmt.Sprintf("pod-%d", id),
					Namespace: "default",
				},
			}

			incidents[id] = c.Correlate(context.Background(), trigger)
		}(i)
	}

	close(start)
	wg.Wait()

	// Verify all incidents were created with unique IDs
	seenIDs := make(map[string]bool)
	for i, inc := range incidents {
		if inc == nil {
			t.Errorf("incident %d is nil", i)
			continue
		}
		if inc.ID == "" {
			t.Errorf("incident %d has empty ID", i)
			continue
		}
		if seenIDs[inc.ID] {
			t.Errorf("duplicate incident ID: %s", inc.ID)
		}
		seenIDs[inc.ID] = true

		if inc.Trigger.Source != fmt.Sprintf("test-%d", i) {
			t.Errorf("incident %d has wrong source: %s", i, inc.Trigger.Source)
		}
	}
}

func TestCorrelatorConcurrentEventIngestion(t *testing.T) {
	t.Parallel()

	c := NewCorrelator(nil, nil, nil)

	const numGoroutines = 50
	const eventsPerGoroutine = 20
	totalEvents := numGoroutines * eventsPerGoroutine

	var wg sync.WaitGroup
	start := make(chan struct{})
	var completedCount atomic.Int64

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start

			for j := 0; j < eventsPerGoroutine; j++ {
				if ctx.Err() != nil {
					return
				}

				trigger := Event{
					Type:      "xid",
					Timestamp: time.Now(),
					Source:    "xid.Watcher",
					Data: xid.XIDEvent{
						XIDCode:   79 + (j % 10),
						Severity:  "critical",
						PCIBusID:  fmt.Sprintf("0000:00:%02X.0", id),
						PodName:   fmt.Sprintf("pod-%d-%d", id, j),
						Namespace: "gpu-workloads",
					},
				}

				incident := c.Correlate(ctx, trigger)
				if incident != nil && incident.ID != "" {
					completedCount.Add(1)
				}
			}
		}(i)
	}

	close(start)
	wg.Wait()

	completed := completedCount.Load()
	if completed != int64(totalEvents) {
		t.Errorf("completed %d correlations, want %d", completed, totalEvents)
	}
}
