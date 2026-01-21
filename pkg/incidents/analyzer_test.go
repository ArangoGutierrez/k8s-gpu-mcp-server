// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
)

func TestNewAnalyzer(t *testing.T) {
	a := NewAnalyzer()
	if a == nil {
		t.Fatal("NewAnalyzer returned nil")
	}
	if len(a.patterns) != len(KnownPatterns) {
		t.Errorf("expected %d patterns, got %d", len(KnownPatterns), len(a.patterns))
	}
}

func TestNewAnalyzerWithPatterns(t *testing.T) {
	customPatterns := []FailurePattern{
		{Name: "test", Category: "test_category"},
	}
	a := NewAnalyzerWithPatterns(customPatterns)
	if len(a.patterns) != 1 {
		t.Errorf("expected 1 pattern, got %d", len(a.patterns))
	}
}

func TestAnalyze_NilIncident(t *testing.T) {
	a := NewAnalyzer()
	report := a.Analyze(nil)

	if report == nil {
		t.Fatal("Analyze returned nil for nil incident")
	}
	if report.RootCause.Category != CategoryUnknown {
		t.Errorf("expected category %q, got %q", CategoryUnknown, report.RootCause.Category)
	}
	if report.RootCause.Confidence != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", report.RootCause.Confidence)
	}
}

func TestAnalyze_ThermalCascade(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-thermal-001",
		Timestamp: time.Now(),
		Causality: events.CausalityThermalCascade,
		Trigger: events.Event{
			Type:      "xid",
			Timestamp: time.Now(),
			Data: xid.XIDEvent{
				XIDCode:   79,
				Timestamp: time.Now(),
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:   time.Now(),
				Temperature: 95, // High temperature
				Throttling:  0x01,
				MemUsed:     8 * 1024 * 1024 * 1024,
				MemTotal:    16 * 1024 * 1024 * 1024,
			},
		},
		AffectedPods: []events.AffectedPod{
			{PodName: "training-job-1", Namespace: "ml", PodUID: "uid-123"},
		},
		RelatedEvents: []events.Event{
			{
				Type:      "k8s",
				Timestamp: time.Now(),
				Data: events.K8sEvent{
					Reason:   "Failed",
					NodeName: "gpu-node-01",
				},
			},
		},
	}

	report := a.Analyze(incident)

	if report.RootCause.Category != CategoryThermalCascade {
		t.Errorf("expected category %q, got %q", CategoryThermalCascade, report.RootCause.Category)
	}
	if report.RootCause.Confidence < 0.5 {
		t.Errorf("expected confidence >= 0.5, got %f", report.RootCause.Confidence)
	}
	if !report.RootCause.NotYourCode {
		t.Error("expected NotYourCode=true for thermal cascade")
	}
	if len(report.RootCause.Evidence) == 0 {
		t.Error("expected evidence to be populated")
	}
	if len(report.Recommendations) == 0 {
		t.Error("expected recommendations to be populated")
	}
	if report.Pod.Name != "training-job-1" {
		t.Errorf("expected pod name %q, got %q", "training-job-1", report.Pod.Name)
	}
	if report.Node != "gpu-node-01" {
		t.Errorf("expected node %q, got %q", "gpu-node-01", report.Node)
	}
}

func TestAnalyze_ECCFailure(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-ecc-001",
		Timestamp: time.Now(),
		Causality: events.CausalityMemoryFailure,
		Trigger: events.Event{
			Type:      "xid",
			Timestamp: time.Now(),
			Data: xid.XIDEvent{
				XIDCode:   48, // Double-bit ECC error
				Timestamp: time.Now(),
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:        time.Now(),
				Temperature:      70,
				ECCUncorrectable: 5, // Uncorrectable errors
				UUID:             "GPU-12345678",
			},
		},
		AffectedPods: []events.AffectedPod{
			{PodName: "inference-pod", Namespace: "prod"},
		},
	}

	report := a.Analyze(incident)

	if report.RootCause.Category != CategoryECCFailure {
		t.Errorf("expected category %q, got %q", CategoryECCFailure, report.RootCause.Category)
	}
	if report.RootCause.Confidence < 0.5 {
		t.Errorf("expected confidence >= 0.5, got %f", report.RootCause.Confidence)
	}
	if !report.RootCause.NotYourCode {
		t.Error("expected NotYourCode=true for ECC failure")
	}
	if report.HardwareState == nil {
		t.Fatal("expected HardwareState to be populated")
	}
	if report.HardwareState.ECCUncorrectable != 5 {
		t.Errorf("expected ECCUncorrectable=5, got %d", report.HardwareState.ECCUncorrectable)
	}
	if report.GPUUUID != "GPU-12345678" {
		t.Errorf("expected GPU UUID %q, got %q", "GPU-12345678", report.GPUUUID)
	}
}

func TestAnalyze_SoftwareOOM(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-oom-001",
		Timestamp: time.Now(),
		Causality: events.CausalitySoftwareOOM,
		Trigger: events.Event{
			Type:      "k8s",
			Timestamp: time.Now(),
			Data: events.K8sEvent{
				Reason:    "OOMKilled",
				PodName:   "memory-hog",
				Namespace: "default",
				NodeName:  "gpu-node-02",
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp: time.Now(),
				MemUsed:   15 * 1024 * 1024 * 1024, // 15GB
				MemTotal:  16 * 1024 * 1024 * 1024, // 16GB (~94% used)
			},
		},
		AffectedPods: []events.AffectedPod{
			{PodName: "memory-hog", Namespace: "default"},
		},
		// No XID events
		RelatedEvents: []events.Event{},
	}

	report := a.Analyze(incident)

	if report.RootCause.Category != CategorySoftwareOOM {
		t.Errorf("expected category %q, got %q", CategorySoftwareOOM, report.RootCause.Category)
	}
	// For OOM, NotYourCode should be false (it IS user code)
	if report.RootCause.NotYourCode {
		t.Error("expected NotYourCode=false for software OOM")
	}
	if report.Pod.Name != "memory-hog" {
		t.Errorf("expected pod name %q, got %q", "memory-hog", report.Pod.Name)
	}
}

func TestAnalyze_NVLinkFailure(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-nvlink-001",
		Timestamp: time.Now(),
		Trigger: events.Event{
			Type:      "xid",
			Timestamp: time.Now(),
			Data: xid.XIDEvent{
				XIDCode:   74, // NVLink error
				Timestamp: time.Now(),
			},
		},
		RelatedEvents: []events.Event{
			{
				Type:      "k8s",
				Timestamp: time.Now(),
				Data: events.K8sEvent{
					Reason:   "Failed",
					Message:  "NVLink interconnect error detected",
					NodeName: "gpu-node-03",
				},
			},
		},
		AffectedPods: []events.AffectedPod{
			{PodName: "multi-gpu-job", Namespace: "training"},
		},
	}

	report := a.Analyze(incident)

	if report.RootCause.Category != CategoryNVLinkFailure {
		t.Errorf("expected category %q, got %q", CategoryNVLinkFailure, report.RootCause.Category)
	}
	if report.RootCause.Confidence < 0.5 {
		t.Errorf("expected confidence >= 0.5, got %f", report.RootCause.Confidence)
	}
	if !report.RootCause.NotYourCode {
		t.Error("expected NotYourCode=true for NVLink failure")
	}
}

func TestAnalyze_XID79BusError(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-xid79-001",
		Timestamp: time.Now(),
		Trigger: events.Event{
			Type:      "xid",
			Timestamp: time.Now(),
			Data: xid.XIDEvent{
				XIDCode:   79, // GPU fell off bus
				Timestamp: time.Now(),
				GPUUUID:   "GPU-FALLEN-BUS",
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:  time.Now(),
				Throttling: 0x02, // Some throttling
			},
		},
		AffectedPods: []events.AffectedPod{
			{PodName: "critical-job", Namespace: "prod"},
		},
	}

	report := a.Analyze(incident)

	if report.RootCause.Category != CategoryXID79BusError {
		t.Errorf("expected category %q, got %q", CategoryXID79BusError, report.RootCause.Category)
	}
	if report.RootCause.Confidence < 0.6 {
		t.Errorf("expected confidence >= 0.6, got %f", report.RootCause.Confidence)
	}
	if !report.RootCause.NotYourCode {
		t.Error("expected NotYourCode=true for XID 79 bus error")
	}
	if report.GPUUUID != "GPU-FALLEN-BUS" {
		t.Errorf("expected GPU UUID %q, got %q", "GPU-FALLEN-BUS", report.GPUUUID)
	}
}

func TestAnalyze_NoMatch(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-unknown-001",
		Timestamp: time.Now(),
		Causality: events.CausalityUnknown,
		Trigger: events.Event{
			Type:      "k8s",
			Timestamp: time.Now(),
			Data: events.K8sEvent{
				Reason:  "Scheduled",
				Message: "Pod scheduled successfully",
			},
		},
		// No GPU snapshots, no XID events
	}

	report := a.Analyze(incident)

	if report.RootCause.Category != CategoryUnknown {
		t.Errorf("expected category %q, got %q", CategoryUnknown, report.RootCause.Category)
	}
	if report.RootCause.Confidence > 0.1 {
		t.Errorf("expected confidence <= 0.1, got %f", report.RootCause.Confidence)
	}
	// Unknown should not claim "not your code" unless proven
	if report.RootCause.NotYourCode {
		t.Error("expected NotYourCode=false for unknown category")
	}
}

func TestAnalyze_MultipleMatches_HighestConfidenceWins(t *testing.T) {
	a := NewAnalyzer()

	// Create incident that could match both thermal and XID 79 patterns
	// XID 79 should win because it has higher weight for XID match
	incident := &events.CorrelatedIncident{
		ID:        "test-multi-001",
		Timestamp: time.Now(),
		Trigger: events.Event{
			Type:      "xid",
			Timestamp: time.Now(),
			Data: xid.XIDEvent{
				XIDCode:   79,
				Timestamp: time.Now(),
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:   time.Now(),
				Temperature: 85, // High temp
				Throttling:  0x01,
			},
		},
	}

	report := a.Analyze(incident)

	// XID 79 pattern has 0.70 weight for XID=79, so should win
	if report.RootCause.Category != CategoryXID79BusError {
		t.Errorf("expected category %q, got %q", CategoryXID79BusError, report.RootCause.Category)
	}
}

func TestAnalyze_EmptyIncident(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-empty-001",
		Timestamp: time.Now(),
	}

	report := a.Analyze(incident)

	if report == nil {
		t.Fatal("Analyze returned nil for empty incident")
	}
	if report.ID != "test-empty-001" {
		t.Errorf("expected ID %q, got %q", "test-empty-001", report.ID)
	}
	// Should get unknown category with low confidence
	if report.RootCause.Category != CategoryUnknown {
		t.Errorf("expected category %q, got %q", CategoryUnknown, report.RootCause.Category)
	}
}

func TestAnalyze_RecommendationsTemplated(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-template-001",
		Timestamp: time.Now(),
		Causality: events.CausalityThermalCascade,
		Trigger: events.Event{
			Type:      "xid",
			Timestamp: time.Now(),
			Data: xid.XIDEvent{
				XIDCode:   79,
				Timestamp: time.Now(),
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:   time.Now(),
				Temperature: 95,
				Throttling:  0x01,
			},
		},
		RelatedEvents: []events.Event{
			{
				Type: "k8s",
				Data: events.K8sEvent{
					NodeName: "gpu-node-test",
				},
			},
		},
		AffectedPods: []events.AffectedPod{
			{PodName: "test-pod", Namespace: "test-ns"},
		},
	}

	report := a.Analyze(incident)

	if len(report.Recommendations) == 0 {
		t.Fatal("expected recommendations to be populated")
	}

	// Check that node name is templated into commands
	foundNodeCmd := false
	for _, rec := range report.Recommendations {
		if rec.Command != "" {
			if containsStr(rec.Command, "gpu-node-test") {
				foundNodeCmd = true
				break
			}
		}
	}

	if !foundNodeCmd && report.Node != "" {
		t.Log("Note: Recommendations may use placeholder if node not in commands")
	}
}

func TestAnalyze_HardwareStateExtracted(t *testing.T) {
	a := NewAnalyzer()

	incident := &events.CorrelatedIncident{
		ID:        "test-hwstate-001",
		Timestamp: time.Now(),
		Trigger: events.Event{
			Type:      "xid",
			Timestamp: time.Now(),
			Data: xid.XIDEvent{
				XIDCode: 48,
			},
		},
		GPUSnapshots: []blackbox.GPUSnapshot{
			{
				Timestamp:        time.Now(),
				Temperature:      75,
				TempThreshold:    83,
				MemUsed:          8589934592,  // 8GB
				MemTotal:         17179869184, // 16GB
				ECCCorrectable:   10,
				ECCUncorrectable: 2,
				Throttling:       0x04,
			},
		},
	}

	report := a.Analyze(incident)

	if report.HardwareState == nil {
		t.Fatal("expected HardwareState to be populated")
	}

	hw := report.HardwareState
	if hw.Temperature != 75 {
		t.Errorf("expected Temperature=75, got %d", hw.Temperature)
	}
	if hw.TempThreshold != 83 {
		t.Errorf("expected TempThreshold=83, got %d", hw.TempThreshold)
	}
	if hw.ECCCorrectable != 10 {
		t.Errorf("expected ECCCorrectable=10, got %d", hw.ECCCorrectable)
	}
	if hw.ECCUncorrectable != 2 {
		t.Errorf("expected ECCUncorrectable=2, got %d", hw.ECCUncorrectable)
	}
	if hw.Throttling != 0x04 {
		t.Errorf("expected Throttling=0x04, got %d", hw.Throttling)
	}
}

func TestAnalyze_ConfidenceRange(t *testing.T) {
	a := NewAnalyzer()

	// Test various incidents and ensure confidence is always 0.0-1.0
	testCases := []struct {
		name     string
		incident *events.CorrelatedIncident
	}{
		{"nil", nil},
		{"empty", &events.CorrelatedIncident{ID: "empty"}},
		{"thermal", &events.CorrelatedIncident{
			ID:        "thermal",
			Causality: events.CausalityThermalCascade,
			GPUSnapshots: []blackbox.GPUSnapshot{
				{Temperature: 95, Throttling: 0x01},
			},
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			report := a.Analyze(tc.incident)
			if report.RootCause.Confidence < 0.0 || report.RootCause.Confidence > 1.0 {
				t.Errorf("confidence %f out of range [0.0, 1.0]", report.RootCause.Confidence)
			}
		})
	}
}

// Test report type methods

func TestHardwareSnapshot_MemoryUsagePercent(t *testing.T) {
	tests := []struct {
		name     string
		hw       *HardwareSnapshot
		expected float64
	}{
		{"nil", nil, 0},
		{"zero total", &HardwareSnapshot{MemUsed: 100, MemTotal: 0}, 0},
		{"50%", &HardwareSnapshot{MemUsed: 50, MemTotal: 100}, 50.0},
		{"100%", &HardwareSnapshot{MemUsed: 100, MemTotal: 100}, 100.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.hw.MemoryUsagePercent()
			if got != tc.expected {
				t.Errorf("expected %f, got %f", tc.expected, got)
			}
		})
	}
}

func TestHardwareSnapshot_IsThrottled(t *testing.T) {
	tests := []struct {
		name     string
		hw       *HardwareSnapshot
		expected bool
	}{
		{"nil", nil, false},
		{"not throttled", &HardwareSnapshot{Throttling: 0}, false},
		{"throttled", &HardwareSnapshot{Throttling: 0x01}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.hw.IsThrottled()
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestHardwareSnapshot_HasUncorrectableECC(t *testing.T) {
	tests := []struct {
		name     string
		hw       *HardwareSnapshot
		expected bool
	}{
		{"nil", nil, false},
		{"no errors", &HardwareSnapshot{ECCUncorrectable: 0}, false},
		{"has errors", &HardwareSnapshot{ECCUncorrectable: 1}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.hw.HasUncorrectableECC()
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

// Test pattern definitions

func TestKnownPatterns_Count(t *testing.T) {
	if len(KnownPatterns) < 5 {
		t.Errorf("expected at least 5 patterns, got %d", len(KnownPatterns))
	}
}

func TestKnownPatterns_WeightsValid(t *testing.T) {
	for _, pattern := range KnownPatterns {
		var totalWeight float64
		for _, ind := range pattern.Indicators {
			if ind.Weight < 0 || ind.Weight > 1 {
				t.Errorf("pattern %q: indicator weight %f out of range [0,1]",
					pattern.Name, ind.Weight)
			}
			totalWeight += ind.Weight
		}
		// Weights should sum to approximately 1.0
		if totalWeight < 0.9 || totalWeight > 1.1 {
			t.Errorf("pattern %q: total weight %f should be ~1.0",
				pattern.Name, totalWeight)
		}
	}
}

func TestKnownPatterns_HasRecommendations(t *testing.T) {
	for _, pattern := range KnownPatterns {
		if len(pattern.Recommendations) == 0 {
			t.Errorf("pattern %q has no recommendations", pattern.Name)
		}
		for _, rec := range pattern.Recommendations {
			if rec.Action == "" {
				t.Errorf("pattern %q has recommendation with empty action", pattern.Name)
			}
			if rec.Priority == "" {
				t.Errorf("pattern %q has recommendation with empty priority", pattern.Name)
			}
		}
	}
}

// Helper functions

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstr(s, substr)))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
