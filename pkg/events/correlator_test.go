// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
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

	incident := c.Correlate(trigger)

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

	// Should have: throttle, ecc, trigger = 3 entries
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

	// Should find 4 pods (trigger has pod-1 but it's K8sEvent in Data, not added from trigger directly)
	// Actually let me trace through: trigger.Data is K8sEvent but identifyAffectedPods only looks at RelatedEvents
	// So we get: pod-2 (k8s), pod-3 (xid), pod-4 (snapshot) = 3 pods
	if len(pods) != 3 {
		t.Errorf("expected 3 affected pods, got %d", len(pods))
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
		types []string
		a, b  string
		want  bool
	}{
		{[]string{"throttle", "xid", "k8s"}, "throttle", "xid", true},
		{[]string{"throttle", "xid", "k8s"}, "xid", "k8s", true},
		{[]string{"throttle", "xid", "k8s"}, "k8s", "throttle", false},
		{[]string{"xid"}, "throttle", "xid", false},
		{[]string{}, "throttle", "xid", false},
		{[]string{"ecc", "temp", "xid"}, "ecc", "xid", true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
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
