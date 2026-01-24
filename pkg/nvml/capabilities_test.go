// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapabilityTier_String(t *testing.T) {
	tests := []struct {
		tier CapabilityTier
		want string
	}{
		{TierUnknown, "unknown"},
		{Tier1Basic, "basic"},
		{Tier2Health, "health"},
		{Tier3Advanced, "advanced"},
		{CapabilityTier(99), "unknown"}, // invalid tier
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.tier.String())
		})
	}
}

func TestCalculateTier(t *testing.T) {
	tests := []struct {
		name      string
		supported map[string]bool
		want      CapabilityTier
	}{
		{
			name:      "empty map returns unknown",
			supported: map[string]bool{},
			want:      TierUnknown,
		},
		{
			name: "missing tier1 API returns unknown",
			supported: map[string]bool{
				APIName:        true,
				APIUUID:        true,
				APIPCIInfo:     true,
				APIMemoryInfo:  true,
				APITemperature: true,
				APIPowerUsage:  true,
				// APIUtilization missing
			},
			want: TierUnknown,
		},
		{
			name: "tier1 only returns Tier1Basic",
			supported: map[string]bool{
				APIName:        true,
				APIUUID:        true,
				APIPCIInfo:     true,
				APIMemoryInfo:  true,
				APITemperature: true,
				APIPowerUsage:  true,
				APIUtilization: true,
			},
			want: Tier1Basic,
		},
		{
			name: "tier1 + tier2 returns Tier2Health",
			supported: map[string]bool{
				// Tier 1
				APIName:        true,
				APIUUID:        true,
				APIPCIInfo:     true,
				APIMemoryInfo:  true,
				APITemperature: true,
				APIPowerUsage:  true,
				APIUtilization: true,
				// Tier 2
				APIPowerLimit:      true,
				APIEccMode:         true,
				APIEccErrors:       true,
				APIThrottleReasons: true,
				APIClockInfo:       true,
				APITempThreshold:   true,
			},
			want: Tier2Health,
		},
		{
			name: "all APIs returns Tier3Advanced",
			supported: map[string]bool{
				// Tier 1
				APIName:        true,
				APIUUID:        true,
				APIPCIInfo:     true,
				APIMemoryInfo:  true,
				APITemperature: true,
				APIPowerUsage:  true,
				APIUtilization: true,
				// Tier 2
				APIPowerLimit:      true,
				APIEccMode:         true,
				APIEccErrors:       true,
				APIThrottleReasons: true,
				APIClockInfo:       true,
				APITempThreshold:   true,
				// Tier 3
				APINVLinkState:      true,
				APINVLinkRemotePCI:  true,
				APINVLinkErrors:     true,
				APIComputeProcesses: true,
			},
			want: Tier3Advanced,
		},
		{
			name: "tier2 missing one API stays at tier1",
			supported: map[string]bool{
				// Tier 1
				APIName:        true,
				APIUUID:        true,
				APIPCIInfo:     true,
				APIMemoryInfo:  true,
				APITemperature: true,
				APIPowerUsage:  true,
				APIUtilization: true,
				// Tier 2 (missing APIClockInfo)
				APIPowerLimit:      true,
				APIEccMode:         true,
				APIEccErrors:       true,
				APIThrottleReasons: true,
				APITempThreshold:   true,
			},
			want: Tier1Basic,
		},
		{
			name: "tier3 missing one API stays at tier2",
			supported: map[string]bool{
				// Tier 1
				APIName:        true,
				APIUUID:        true,
				APIPCIInfo:     true,
				APIMemoryInfo:  true,
				APITemperature: true,
				APIPowerUsage:  true,
				APIUtilization: true,
				// Tier 2
				APIPowerLimit:      true,
				APIEccMode:         true,
				APIEccErrors:       true,
				APIThrottleReasons: true,
				APIClockInfo:       true,
				APITempThreshold:   true,
				// Tier 3 (missing APIComputeProcesses)
				APINVLinkState:     true,
				APINVLinkRemotePCI: true,
				APINVLinkErrors:    true,
			},
			want: Tier2Health,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateTier(tt.supported)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildCapabilities(t *testing.T) {
	supported := map[string]bool{
		APIName:        true,
		APIUUID:        true,
		APIPCIInfo:     true,
		APIMemoryInfo:  true,
		APITemperature: true,
		APIPowerUsage:  true,
		APIUtilization: true,
	}

	caps := buildCapabilities(supported, "535.129.03", "12.2")

	assert.Equal(t, Tier1Basic, caps.Tier)
	assert.Equal(t, "535.129.03", caps.DriverVersion)
	assert.Equal(t, "12.2", caps.CudaVersion)
	assert.Len(t, caps.SupportedAPIs, 7)
	assert.NotEmpty(t, caps.UnsupportedAPIs)
}

func TestCapabilities_SupportsAPI(t *testing.T) {
	caps := &Capabilities{
		Tier:          Tier2Health,
		SupportedAPIs: []string{APIName, APIUUID, APIEccMode},
	}

	assert.True(t, caps.SupportsAPI(APIName))
	assert.True(t, caps.SupportsAPI(APIEccMode))
	assert.False(t, caps.SupportsAPI(APINVLinkState))
}

func TestCapabilities_IsDegraded(t *testing.T) {
	tests := []struct {
		tier     CapabilityTier
		degraded bool
	}{
		{TierUnknown, true},
		{Tier1Basic, true},
		{Tier2Health, true},
		{Tier3Advanced, false},
	}

	for _, tt := range tests {
		t.Run(tt.tier.String(), func(t *testing.T) {
			caps := &Capabilities{Tier: tt.tier}
			assert.Equal(t, tt.degraded, caps.IsDegraded())
		})
	}
}

func TestCapabilities_DegradedReason(t *testing.T) {
	tests := []struct {
		tier   CapabilityTier
		driver string
		expect string
	}{
		{TierUnknown, "", "capability detection failed"},
		{Tier1Basic, "450.80.02", "450.80.02"},
		{Tier2Health, "535.129.03", "535.129.03"},
		{Tier3Advanced, "575.57.08", ""},
	}

	for _, tt := range tests {
		t.Run(tt.tier.String(), func(t *testing.T) {
			caps := &Capabilities{Tier: tt.tier, DriverVersion: tt.driver}
			reason := caps.DegradedReason()
			if tt.expect == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tt.expect)
			}
		})
	}
}

func TestMock_GetCapabilities(t *testing.T) {
	ctx := context.Background()
	mock := NewMock(2)

	caps, err := mock.GetCapabilities(ctx)
	require.NoError(t, err)
	require.NotNil(t, caps)

	assert.Equal(t, Tier3Advanced, caps.Tier)
	assert.Equal(t, "575.57.08", caps.DriverVersion)
	assert.Equal(t, "12.9", caps.CudaVersion)
	assert.NotEmpty(t, caps.SupportedAPIs)
}

func TestMock_SetCapabilities(t *testing.T) {
	ctx := context.Background()
	mock := NewMock(1)

	// Simulate degraded mode (Tier1Basic)
	mock.SetCapabilities(&Capabilities{
		Tier:            Tier1Basic,
		DriverVersion:   "450.80.02",
		SupportedAPIs:   Tier1APIs,
		UnsupportedAPIs: append(Tier2APIs, Tier3APIs...),
	})

	caps, err := mock.GetCapabilities(ctx)
	require.NoError(t, err)

	assert.Equal(t, Tier1Basic, caps.Tier)
	assert.Equal(t, "450.80.02", caps.DriverVersion)
	assert.True(t, caps.IsDegraded())
	assert.Contains(t, caps.DegradedReason(), "450.80.02")
}
