// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncidentReport_JSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	original := IncidentReport{
		ID:        "inc-001",
		Timestamp: ts,
		Pod: PodInfo{
			Name:      "training-pod-abc",
			Namespace: "ml-jobs",
			UID:       "uid-12345",
		},
		Node:    "gpu-node-01",
		GPUUUID: "GPU-abc-123",
		RootCause: RootCause{
			Category:    CategoryThermalCascade,
			Confidence:  0.85,
			Evidence:    []string{"GPU temp 95C", "hw_thermal throttle active"},
			NotYourCode: true,
		},
		Timeline: []events.TimelineEntry{
			{
				Timestamp:    ts.Add(-5 * time.Minute),
				RelativeTime: "-5m",
				EventType:    "temp",
				Description:  "GPU temperature exceeded 82C",
				Severity:     "warning",
			},
			{
				Timestamp:    ts,
				RelativeTime: "0s",
				EventType:    "xid",
				Description:  "XID 79 - GPU fell off bus",
				Severity:     "critical",
			},
		},
		HardwareState: &HardwareSnapshot{
			Temperature:      95,
			TempThreshold:    83,
			MemUsed:          16_000_000_000,
			MemTotal:         40_000_000_000,
			ECCCorrectable:   0,
			ECCUncorrectable: 0,
			Throttling:       0x0000000000000008,
		},
		Recommendations: []Recommendation{
			{
				Action:   "Check node cooling",
				Command:  "kubectl describe node gpu-node-01",
				Priority: PriorityHigh,
			},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "marshal should succeed")

	var decoded IncidentReport
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err, "unmarshal should succeed")

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Timestamp.UTC(), decoded.Timestamp.UTC())
	assert.Equal(t, original.Pod, decoded.Pod)
	assert.Equal(t, original.Node, decoded.Node)
	assert.Equal(t, original.GPUUUID, decoded.GPUUUID)
	assert.Equal(t, original.RootCause.Category, decoded.RootCause.Category)
	assert.InDelta(t, original.RootCause.Confidence, decoded.RootCause.Confidence, 0.001)
	assert.Equal(t, original.RootCause.Evidence, decoded.RootCause.Evidence)
	assert.Equal(t, original.RootCause.NotYourCode, decoded.RootCause.NotYourCode)
	assert.Len(t, decoded.Timeline, 2)
	assert.Len(t, decoded.Recommendations, 1)
	require.NotNil(t, decoded.HardwareState)
	assert.Equal(t, original.HardwareState.Temperature, decoded.HardwareState.Temperature)
	assert.Equal(t, original.HardwareState.MemUsed, decoded.HardwareState.MemUsed)
}

func TestRootCause_JSONRoundTrip(t *testing.T) {
	original := RootCause{
		Category:    CategoryECCFailure,
		Confidence:  0.92,
		Evidence:    []string{"uncorrectable ECC > 0", "XID 63 detected"},
		NotYourCode: true,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded RootCause
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Category, decoded.Category)
	assert.InDelta(t, original.Confidence, decoded.Confidence, 0.001)
	assert.Equal(t, original.Evidence, decoded.Evidence)
	assert.Equal(t, original.NotYourCode, decoded.NotYourCode)
}

func TestRecommendation_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		rec  Recommendation
	}{
		{
			name: "with command",
			rec: Recommendation{
				Action:   "Drain the node",
				Command:  "kubectl drain gpu-node-01",
				Priority: PriorityHigh,
			},
		},
		{
			name: "without command",
			rec: Recommendation{
				Action:   "Schedule GPU replacement",
				Priority: PriorityMedium,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.rec)
			require.NoError(t, err)

			var decoded Recommendation
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.rec.Action, decoded.Action)
			assert.Equal(t, tt.rec.Command, decoded.Command)
			assert.Equal(t, tt.rec.Priority, decoded.Priority)

			// Verify omitempty for Command
			if tt.rec.Command == "" {
				assert.NotContains(t, string(data), `"command"`)
			}
		})
	}
}

func TestIncidentReport_ZeroValue(t *testing.T) {
	var report IncidentReport

	data, err := json.Marshal(report)
	require.NoError(t, err, "zero-value struct should marshal")

	var decoded IncidentReport
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err, "zero-value struct should unmarshal")

	assert.Empty(t, decoded.ID)
	assert.Empty(t, decoded.Node)
	assert.Empty(t, decoded.GPUUUID)
	assert.Empty(t, decoded.RootCause.Category)
	assert.Zero(t, decoded.RootCause.Confidence)
	assert.Nil(t, decoded.HardwareState)
	assert.Nil(t, decoded.Recommendations)
	assert.Nil(t, decoded.Timeline)
}

func TestPodInfo_JSONOmitEmpty(t *testing.T) {
	pod := PodInfo{
		Name:      "test-pod",
		Namespace: "default",
	}

	data, err := json.Marshal(pod)
	require.NoError(t, err)

	// UID has omitempty; should not be present when empty
	assert.NotContains(t, string(data), `"uid"`)
	assert.Contains(t, string(data), `"name"`)
	assert.Contains(t, string(data), `"namespace"`)
}

func TestHardwareSnapshot_JSONRoundTrip(t *testing.T) {
	original := HardwareSnapshot{
		Temperature:      85,
		TempThreshold:    83,
		MemUsed:          32_000_000_000,
		MemTotal:         40_000_000_000,
		ECCCorrectable:   5,
		ECCUncorrectable: 1,
		Throttling:       0x0000000000000008,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded HardwareSnapshot
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original, decoded)
}
