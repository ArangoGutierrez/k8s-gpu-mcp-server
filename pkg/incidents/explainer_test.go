// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"strings"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
)

func TestNewExplainer(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	if e == nil {
		t.Fatal("NewExplainer returned nil")
	}
	if len(e.explanationTemplates) == 0 {
		t.Error("No explanation templates loaded")
	}
	if len(e.summaryTemplates) == 0 {
		t.Error("No summary templates loaded")
	}
}

func TestGenerateExplanation_ThermalCascade(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	incident := &events.CorrelatedIncident{
		ID:        "inc-test-thermal",
		Timestamp: now,
		Causality: events.CausalityThermalCascade,
		AffectedPods: []events.AffectedPod{
			{PodName: "training-job-xyz", Namespace: "ml"},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:     now,
				Temperature:   94,
				TempThreshold: 90,
				Throttling:    0x40, // thermal throttle bit
			},
		},
		RelatedEvents: []events.Event{
			{
				Type:      "xid",
				Timestamp: now,
				Data: xid.XIDEvent{
					XIDCode:     79,
					Description: "GPU fell off the bus",
				},
			},
			{
				Type:      "k8s",
				Timestamp: now,
				Data: events.K8sEvent{
					NodeName: "gpu-node-01",
				},
			},
		},
	}

	explanation := e.GenerateExplanation(incident)

	// Verify key phrases
	checks := []string{
		"training-job-xyz",
		"GPU overheating",
		"not a bug in your code",
		"94°C",
		"XID 79",
		"kubectl cordon",
	}

	for _, check := range checks {
		if !strings.Contains(explanation, check) {
			t.Errorf("Explanation missing %q:\n%s", check, explanation)
		}
	}
}

func TestGenerateExplanation_MemoryFailure(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	incident := &events.CorrelatedIncident{
		ID:        "inc-test-memory",
		Timestamp: now,
		Causality: events.CausalityMemoryFailure,
		AffectedPods: []events.AffectedPod{
			{PodName: "model-training", Namespace: "ml"},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:        now,
				ECCUncorrectable: 5,
				ECCCorrectable:   100,
			},
		},
		RelatedEvents: []events.Event{
			{
				Type:      "xid",
				Timestamp: now,
				Data: xid.XIDEvent{
					XIDCode:     48,
					Description: "Double Bit ECC Error",
				},
			},
			{
				Type:      "k8s",
				Timestamp: now,
				Data: events.K8sEvent{
					NodeName: "gpu-node-02",
				},
			},
		},
	}

	explanation := e.GenerateExplanation(incident)

	checks := []string{
		"model-training",
		"memory hardware failure",
		"5 uncorrectable ECC errors",
		"not a problem with your code",
		"kubectl drain",
	}

	for _, check := range checks {
		if !strings.Contains(explanation, check) {
			t.Errorf("Explanation missing %q:\n%s", check, explanation)
		}
	}
}

func TestGenerateExplanation_SoftwareOOM(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	incident := &events.CorrelatedIncident{
		ID:        "inc-test-oom",
		Timestamp: now,
		Causality: events.CausalitySoftwareOOM,
		AffectedPods: []events.AffectedPod{
			{PodName: "model-training", Namespace: "ml"},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp: now,
				MemUsed:   15 * 1024 * 1024 * 1024, // 15 GB
				MemTotal:  16 * 1024 * 1024 * 1024, // 16 GB
			},
		},
	}

	explanation := e.GenerateExplanation(incident)

	checks := []string{
		"model-training",
		"ran out of GPU memory",
		"15.0 GB",
		"16.0 GB",
		"code/configuration issue",
		"batch size",
	}

	for _, check := range checks {
		if !strings.Contains(explanation, check) {
			t.Errorf("Explanation missing %q:\n%s", check, explanation)
		}
	}
}

func TestGenerateExplanation_Unknown(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	incident := &events.CorrelatedIncident{
		ID:        "inc-test-unknown",
		Timestamp: now,
		Causality: events.CausalityUnknown,
		RelatedEvents: []events.Event{
			{
				Type:      "xid",
				Timestamp: now,
				Data: xid.XIDEvent{
					XIDCode:     31,
					Description: "GPU Exception",
				},
			},
			{
				Type:      "k8s",
				Timestamp: now,
				Data: events.K8sEvent{
					NodeName: "gpu-node-03",
				},
			},
		},
	}

	explanation := e.GenerateExplanation(incident)

	checks := []string{
		"GPU-related failure",
		"XID 31",
		"Manual investigation recommended",
		"nvidia-smi",
	}

	for _, check := range checks {
		if !strings.Contains(explanation, check) {
			t.Errorf("Explanation missing %q:\n%s", check, explanation)
		}
	}
}

func TestGenerateExplanation_NilIncident(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	explanation := e.GenerateExplanation(nil)

	if explanation != "No incident data available." {
		t.Errorf("Unexpected explanation for nil incident: %s", explanation)
	}
}

func TestGenerateExplanation_NoPod(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	incident := &events.CorrelatedIncident{
		ID:        "inc-no-pod",
		Timestamp: now,
		Causality: events.CausalitySoftwareOOM,
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp: now,
				MemUsed:   15e9,
				MemTotal:  16e9,
			},
		},
	}

	explanation := e.GenerateExplanation(incident)

	if !strings.Contains(explanation, "workload") {
		t.Errorf("Expected 'workload' fallback when no pod name:\n%s", explanation)
	}
}

func TestGenerateSummary(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	tests := []struct {
		name      string
		causality string
		podName   string
		wantPart  string
	}{
		{"thermal", events.CausalityThermalCascade, "my-pod", "GPU overheating"},
		{"memory", events.CausalityMemoryFailure, "my-pod", "memory hardware failure"},
		{"oom", events.CausalitySoftwareOOM, "my-pod", "ran out of GPU memory"},
		{"unknown", events.CausalityUnknown, "", "manual investigation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			incident := &events.CorrelatedIncident{
				Timestamp: now,
				Causality: tt.causality,
				GPUSnapshots: []blackbox.GPUSnapshot{
					{MemUsed: 15e9, MemTotal: 16e9},
				},
			}
			if tt.podName != "" {
				incident.AffectedPods = []events.AffectedPod{{PodName: tt.podName}}
			}

			summary := e.GenerateSummary(incident)
			if !strings.Contains(summary, tt.wantPart) {
				t.Errorf("Summary missing %q: %s", tt.wantPart, summary)
			}
		})
	}
}

func TestGenerateSummary_NilIncident(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	summary := e.GenerateSummary(nil)

	if summary != "No incident data available." {
		t.Errorf("Unexpected summary for nil incident: %s", summary)
	}
}

func TestGenerateTimeline(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	incident := &events.CorrelatedIncident{
		ID:        "inc-timeline",
		Timestamp: now,
		Timeline: []events.TimelineEntry{
			{
				Timestamp:    now.Add(-5 * time.Minute),
				RelativeTime: "-5m",
				EventType:    "temp",
				Description:  "High temperature detected",
				Severity:     "warning",
			},
			{
				Timestamp:    now.Add(-2 * time.Minute),
				RelativeTime: "-2m",
				EventType:    "throttle",
				Description:  "Thermal throttling active",
				Severity:     "warning",
			},
			{
				Timestamp:    now,
				RelativeTime: "0s",
				EventType:    "xid",
				Description:  "XID 79: GPU fell off the bus",
				Severity:     "fatal",
			},
		},
	}

	timeline := e.GenerateTimeline(incident)

	checks := []string{
		"## Timeline",
		"(-5m)",
		"(-2m)",
		"(0s)",
		"High temperature",
		"throttling",
		"XID 79",
	}

	for _, check := range checks {
		if !strings.Contains(timeline, check) {
			t.Errorf("Timeline missing %q:\n%s", check, timeline)
		}
	}
}

func TestGenerateTimeline_Empty(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}

	// Nil incident
	timeline := e.GenerateTimeline(nil)
	if timeline != "No timeline data available." {
		t.Errorf("Unexpected timeline for nil incident: %s", timeline)
	}

	// Empty timeline
	timeline = e.GenerateTimeline(&events.CorrelatedIncident{})
	if timeline != "No timeline data available." {
		t.Errorf("Unexpected timeline for empty timeline: %s", timeline)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes uint64
		want  string
	}{
		{500, "500 bytes"},
		{1024, "1.0 KB"},
		{1536 * 1024, "1.5 MB"},
		{16 * 1024 * 1024 * 1024, "16.0 GB"},
		{2 * 1024 * 1024 * 1024 * 1024, "2.0 TB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{30 * time.Second, "30 seconds"},
		{1 * time.Second, "1 second"},
		{3 * time.Minute, "3 minutes"},
		{1 * time.Minute, "1 minute"},
		{2 * time.Hour, "2 hours"},
		{1 * time.Hour, "1 hour"},
		{500 * time.Millisecond, "500 ms"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.duration)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
		}
	}
}

func TestFormatRelativeTime(t *testing.T) {
	base := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		target time.Time
		want   string
	}{
		{base, "0s"},
		{base.Add(30 * time.Second), "+30s"},
		{base.Add(-30 * time.Second), "-30s"},
		{base.Add(5 * time.Minute), "+5m"},
		{base.Add(-5 * time.Minute), "-5m"},
		{base.Add(2 * time.Hour), "+2h"},
		{base.Add(-2 * time.Hour), "-2h"},
		{base.Add(500 * time.Millisecond), "+500ms"},
	}

	for _, tt := range tests {
		got := formatRelativeTime(tt.target, base)
		if got != tt.want {
			t.Errorf("formatRelativeTime(%v, %v) = %q, want %q", tt.target, base, got, tt.want)
		}
	}
}

func TestExtractData_Causality(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	tests := []struct {
		causality    string
		wantHuman    string
		wantNotYours bool
	}{
		{events.CausalityThermalCascade, "hardware thermal issue", true},
		{events.CausalityMemoryFailure, "hardware memory failure", true},
		{events.CausalitySoftwareOOM, "GPU memory exhaustion", false},
		{events.CausalityUnknown, "unknown failure", false},
		{"some_other_causality", "unknown failure", false},
	}

	for _, tt := range tests {
		t.Run(tt.causality, func(t *testing.T) {
			incident := &events.CorrelatedIncident{
				Timestamp: now,
				Causality: tt.causality,
			}
			data := e.extractData(incident)

			if data.CausalityHuman != tt.wantHuman {
				t.Errorf("CausalityHuman = %q, want %q", data.CausalityHuman, tt.wantHuman)
			}
			if data.NotYourCode != tt.wantNotYours {
				t.Errorf("NotYourCode = %v, want %v", data.NotYourCode, tt.wantNotYours)
			}
		})
	}
}

func TestSelectTemplateKey(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}

	tests := []struct {
		causality string
		wantKey   string
	}{
		{events.CausalityThermalCascade, "hardware_thermal"},
		{events.CausalityMemoryFailure, "hardware_memory"},
		{events.CausalitySoftwareOOM, "software_oom"},
		{events.CausalityUnknown, "unknown"},
		{"anything_else", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.causality, func(t *testing.T) {
			got := e.selectTemplateKey(tt.causality)
			if got != tt.wantKey {
				t.Errorf("selectTemplateKey(%q) = %q, want %q", tt.causality, got, tt.wantKey)
			}
		})
	}
}

func TestFindClosestSnapshot(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	base := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)

	snapshots := []blackbox.GPUSnapshot{
		{Timestamp: base.Add(-5 * time.Minute), Temperature: 80},
		{Timestamp: base.Add(-1 * time.Minute), Temperature: 90},
		{Timestamp: base.Add(1 * time.Minute), Temperature: 85},
	}

	// Target exactly at base
	closest := e.findClosestSnapshot(snapshots, base)
	if closest.Temperature != 90 {
		t.Errorf("Expected snapshot at -1m (temp 90), got temp %d", closest.Temperature)
	}

	// Target at -4m (should get -5m snapshot)
	closest = e.findClosestSnapshot(snapshots, base.Add(-4*time.Minute))
	if closest.Temperature != 80 {
		t.Errorf("Expected snapshot at -5m (temp 80), got temp %d", closest.Temperature)
	}

	// Empty snapshots
	closest = e.findClosestSnapshot(nil, base)
	if closest.Temperature != 0 {
		t.Errorf("Expected zero snapshot for empty input, got temp %d", closest.Temperature)
	}
}

func TestCalculateThrottleDuration(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	base := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)

	// With throttling
	snapshots := []blackbox.GPUSnapshot{
		{Timestamp: base, Throttling: 0},
		{Timestamp: base.Add(1 * time.Minute), Throttling: 0x40},
		{Timestamp: base.Add(2 * time.Minute), Throttling: 0x40},
		{Timestamp: base.Add(3 * time.Minute), Throttling: 0x40},
		{Timestamp: base.Add(4 * time.Minute), Throttling: 0},
	}

	duration := e.calculateThrottleDuration(snapshots)
	if duration != "2 minutes" {
		t.Errorf("Expected '2 minutes', got %q", duration)
	}

	// No throttling
	noThrottle := []blackbox.GPUSnapshot{
		{Timestamp: base, Throttling: 0},
		{Timestamp: base.Add(1 * time.Minute), Throttling: 0},
	}
	duration = e.calculateThrottleDuration(noThrottle)
	if duration != "" {
		t.Errorf("Expected empty string for no throttling, got %q", duration)
	}

	// Very short throttling (< 1 second)
	shortThrottle := []blackbox.GPUSnapshot{
		{Timestamp: base, Throttling: 0x40},
		{Timestamp: base.Add(100 * time.Millisecond), Throttling: 0x40},
	}
	duration = e.calculateThrottleDuration(shortThrottle)
	if duration != "" {
		t.Errorf("Expected empty string for short throttling, got %q", duration)
	}
}

func TestExtractNode(t *testing.T) {
	e, err := NewExplainer()
	if err != nil {
		t.Fatalf("NewExplainer returned error: %v", err)
	}
	now := time.Now()

	// From trigger
	incident := &events.CorrelatedIncident{
		Timestamp: now,
		Trigger: events.Event{
			Type: "k8s",
			Data: events.K8sEvent{NodeName: "node-from-trigger"},
		},
	}
	data := e.extractData(incident)
	if data.Node != "node-from-trigger" {
		t.Errorf("Expected node from trigger, got %q", data.Node)
	}

	// From related events
	incident = &events.CorrelatedIncident{
		Timestamp: now,
		Trigger:   events.Event{Type: "xid"},
		RelatedEvents: []events.Event{
			{Type: "k8s", Data: events.K8sEvent{NodeName: "node-from-related"}},
		},
	}
	data = e.extractData(incident)
	if data.Node != "node-from-related" {
		t.Errorf("Expected node from related events, got %q", data.Node)
	}

	// Fallback
	incident = &events.CorrelatedIncident{
		Timestamp: now,
		Trigger:   events.Event{Type: "xid"},
	}
	data = e.extractData(incident)
	if data.Node != "the GPU node" {
		t.Errorf("Expected fallback node name, got %q", data.Node)
	}
}

func TestAbsDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  time.Duration
	}{
		{5 * time.Second, 5 * time.Second},
		{-5 * time.Second, 5 * time.Second},
		{0, 0},
	}

	for _, tt := range tests {
		got := absDuration(tt.input)
		if got != tt.want {
			t.Errorf("absDuration(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
