// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package blackbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGPUSnapshot_IsThrottled(t *testing.T) {
	tests := []struct {
		name       string
		throttling uint64
		want       bool
	}{
		{
			name:       "no throttling",
			throttling: 0,
			want:       false,
		},
		{
			name:       "thermal throttling",
			throttling: 0x0000000000000040, // HwThermalSlowdown
			want:       true,
		},
		{
			name:       "power throttling",
			throttling: 0x0000000000000004, // SwPowerCap
			want:       true,
		},
		{
			name:       "software thermal slowdown",
			throttling: 0x0000000000000020,
			want:       true,
		},
		{
			name:       "hardware slowdown",
			throttling: 0x0000000000000008,
			want:       true,
		},
		{
			name:       "hardware power brake",
			throttling: 0x0000000000000080,
			want:       true,
		},
		{
			name:       "multiple throttle reasons",
			throttling: 0x0000000000000044, // SwPowerCap | HwThermalSlowdown
			want:       true,
		},
		{
			name:       "all bits set",
			throttling: 0xFFFFFFFFFFFFFFFF,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := GPUSnapshot{Throttling: tt.throttling}
			assert.Equal(t, tt.want, s.IsThrottled())
		})
	}
}

func TestGPUSnapshot_HasECCErrors(t *testing.T) {
	tests := []struct {
		name          string
		correctable   uint64
		uncorrectable uint64
		wantHasErrors bool
	}{
		{
			name:          "no errors",
			correctable:   0,
			uncorrectable: 0,
			wantHasErrors: false,
		},
		{
			name:          "single-bit correctable only",
			correctable:   1,
			uncorrectable: 0,
			wantHasErrors: true,
		},
		{
			name:          "double-bit uncorrectable only",
			correctable:   0,
			uncorrectable: 1,
			wantHasErrors: true,
		},
		{
			name:          "both correctable and uncorrectable",
			correctable:   5,
			uncorrectable: 2,
			wantHasErrors: true,
		},
		{
			name:          "large correctable count",
			correctable:   1000000,
			uncorrectable: 0,
			wantHasErrors: true,
		},
		{
			name:          "large uncorrectable count",
			correctable:   0,
			uncorrectable: 999999,
			wantHasErrors: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := GPUSnapshot{
				ECCCorrectable:   tt.correctable,
				ECCUncorrectable: tt.uncorrectable,
			}
			assert.Equal(t, tt.wantHasErrors, s.HasECCErrors())
		})
	}
}

func TestGPUSnapshot_MemoryUsagePercent(t *testing.T) {
	tests := []struct {
		name     string
		memUsed  uint64
		memTotal uint64
		want     float64
	}{
		{
			name:     "zero total returns 0",
			memUsed:  0,
			memTotal: 0,
			want:     0,
		},
		{
			name:     "zero used with nonzero total",
			memUsed:  0,
			memTotal: 40 * 1024 * 1024 * 1024, // 40 GB
			want:     0,
		},
		{
			name:     "full memory",
			memUsed:  40 * 1024 * 1024 * 1024,
			memTotal: 40 * 1024 * 1024 * 1024,
			want:     100,
		},
		{
			name:     "50 percent usage",
			memUsed:  20 * 1024 * 1024 * 1024,
			memTotal: 40 * 1024 * 1024 * 1024,
			want:     50,
		},
		{
			name:     "small usage",
			memUsed:  1,
			memTotal: 1000,
			want:     0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := GPUSnapshot{
				MemUsed:  tt.memUsed,
				MemTotal: tt.memTotal,
			}
			assert.InDelta(t, tt.want, s.MemoryUsagePercent(), 0.001)
		})
	}
}

func TestGPUSnapshot_PowerUsagePercent(t *testing.T) {
	tests := []struct {
		name         string
		powerMW      uint32
		powerLimitMW uint32
		want         float64
	}{
		{
			name:         "zero power limit returns 0",
			powerMW:      0,
			powerLimitMW: 0,
			want:         0,
		},
		{
			name:         "zero power draw",
			powerMW:      0,
			powerLimitMW: 400000, // 400W
			want:         0,
		},
		{
			name:         "at power limit",
			powerMW:      400000,
			powerLimitMW: 400000,
			want:         100,
		},
		{
			name:         "50 percent power",
			powerMW:      200000,
			powerLimitMW: 400000,
			want:         50,
		},
		{
			name:         "maximum power draw exceeding limit",
			powerMW:      450000,
			powerLimitMW: 400000,
			want:         112.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := GPUSnapshot{
				PowerMW:      tt.powerMW,
				PowerLimitMW: tt.powerLimitMW,
			}
			assert.InDelta(t, tt.want, s.PowerUsagePercent(), 0.001)
		})
	}
}

func TestGPUSnapshot_GetTimestamp_ReturnsCorrectTime(t *testing.T) {
	now := time.Now()
	s := GPUSnapshot{Timestamp: now}
	assert.Equal(t, now, s.GetTimestamp())
}

func TestGPUSnapshot_BoundaryValues(t *testing.T) {
	t.Run("zero utilization", func(t *testing.T) {
		s := GPUSnapshot{
			GPUUtil: 0,
			MemUtil: 0,
		}
		assert.Equal(t, uint32(0), s.GPUUtil)
		assert.Equal(t, uint32(0), s.MemUtil)
	})

	t.Run("100 percent utilization", func(t *testing.T) {
		s := GPUSnapshot{
			GPUUtil: 100,
			MemUtil: 100,
		}
		assert.Equal(t, uint32(100), s.GPUUtil)
		assert.Equal(t, uint32(100), s.MemUtil)
	})

	t.Run("full snapshot with all boundary values", func(t *testing.T) {
		s := GPUSnapshot{
			Timestamp:        time.Now(),
			Index:            0,
			UUID:             "GPU-00000000-0000-0000-0000-000000000000",
			Temperature:      0,
			TempThreshold:    90,
			PowerMW:          0,
			PowerLimitMW:     400000,
			MemUsed:          0,
			MemTotal:         0,
			GPUUtil:          0,
			MemUtil:          0,
			SMClock:          0,
			MemClock:         0,
			Throttling:       0,
			ECCCorrectable:   0,
			ECCUncorrectable: 0,
		}

		assert.False(t, s.IsThrottled())
		assert.False(t, s.HasECCErrors())
		assert.Equal(t, float64(0), s.MemoryUsagePercent())
		assert.Equal(t, float64(0), s.PowerUsagePercent())
	})
}
