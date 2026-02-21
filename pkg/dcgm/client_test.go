// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

//go:build !dcgm_cgo

package dcgm

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		logger   *slog.Logger
	}{
		{
			name:     "with endpoint and logger",
			endpoint: "localhost:5555",
			logger:   slog.Default(),
		},
		{
			name:     "with nil logger defaults to slog.Default",
			endpoint: "/var/run/dcgm.sock",
			logger:   nil,
		},
		{
			name:     "with empty endpoint",
			endpoint: "",
			logger:   slog.Default(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(tt.endpoint, tt.logger)
			require.NotNil(t, c)
			assert.Equal(t, tt.endpoint, c.Endpoint())
			assert.False(t, c.IsConnected())
			assert.NotNil(t, c.watchers)
			assert.NotNil(t, c.cache)
		})
	}
}

func TestClient_Connect(t *testing.T) {
	t.Run("successful connect", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		err := c.Connect(context.Background())
		require.NoError(t, err)
		assert.True(t, c.IsConnected())
	})

	t.Run("double connect is no-op", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))
		err := c.Connect(context.Background())
		require.NoError(t, err)
		assert.True(t, c.IsConnected())
	})
}

func TestClient_Disconnect(t *testing.T) {
	t.Run("disconnect after connect", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))
		assert.True(t, c.IsConnected())

		err := c.Disconnect()
		require.NoError(t, err)
		assert.False(t, c.IsConnected())
	})

	t.Run("disconnect when not connected is no-op", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		err := c.Disconnect()
		require.NoError(t, err)
		assert.False(t, c.IsConnected())
	})

	t.Run("disconnect clears watchers and cache", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		// Set up watchers and cache
		require.NoError(t, c.WatchFields(0, []FieldID{FieldGPUTemp}, time.Second))
		c.SetCachedValue(0, FieldGPUTemp, Value{FieldID: FieldGPUTemp, Status: ValueStatusOK, Int64: 55})

		require.NoError(t, c.Disconnect())
		assert.Empty(t, c.watchers)
		assert.Empty(t, c.cache)
	})
}

func TestClient_Reconnect(t *testing.T) {
	t.Run("reconnect when connected is no-op", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		err := c.Reconnect(context.Background())
		require.NoError(t, err)
		assert.True(t, c.IsConnected())
	})

	t.Run("reconnect when not connected returns error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		err := c.Reconnect(context.Background())
		require.ErrorIs(t, err, ErrNotInitialized)
	})
}

func TestClient_IsPlaceholder(t *testing.T) {
	c := NewClient("localhost:5555", nil)
	assert.True(t, c.IsPlaceholder())
}

func TestClient_WatchFields(t *testing.T) {
	tests := []struct {
		name      string
		connected bool
		gpuID     int
		fields    []FieldID
		interval  time.Duration
		wantErr   error
	}{
		{
			name:      "valid watch",
			connected: true,
			gpuID:     0,
			fields:    []FieldID{FieldGPUTemp, FieldPowerUsage},
			interval:  time.Second,
			wantErr:   nil,
		},
		{
			name:      "not connected",
			connected: false,
			gpuID:     0,
			fields:    []FieldID{FieldGPUTemp},
			interval:  time.Second,
			wantErr:   ErrNotInitialized,
		},
		{
			name:      "interval too short",
			connected: true,
			gpuID:     0,
			fields:    []FieldID{FieldGPUTemp},
			interval:  50 * time.Millisecond,
			wantErr:   ErrIntervalTooShort,
		},
		{
			name:      "interval at minimum",
			connected: true,
			gpuID:     0,
			fields:    []FieldID{FieldGPUTemp},
			interval:  100 * time.Millisecond,
			wantErr:   nil,
		},
		{
			name:      "empty fields",
			connected: true,
			gpuID:     0,
			fields:    []FieldID{},
			interval:  time.Second,
			wantErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient("localhost:5555", nil)
			if tt.connected {
				require.NoError(t, c.Connect(context.Background()))
			}

			err := c.WatchFields(tt.gpuID, tt.fields, tt.interval)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestClient_GetLatestValues(t *testing.T) {
	t.Run("not connected returns error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		_, err := c.GetLatestValues(0, []FieldID{FieldGPUTemp})
		require.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("returns not-supported for unwatched fields", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		vals, err := c.GetLatestValues(0, []FieldID{FieldGPUTemp, FieldPowerUsage})
		require.NoError(t, err)
		require.Len(t, vals, 2)

		for _, v := range vals {
			assert.Equal(t, ValueStatusNotSupported, v.Status)
		}
	})

	t.Run("returns cached values", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		c.SetCachedValue(0, FieldGPUTemp, Value{
			FieldID: FieldGPUTemp,
			Status:  ValueStatusOK,
			Int64:   65,
		})

		vals, err := c.GetLatestValues(0, []FieldID{FieldGPUTemp})
		require.NoError(t, err)
		require.Len(t, vals, 1)

		v := vals[FieldGPUTemp]
		assert.Equal(t, ValueStatusOK, v.Status)
		assert.Equal(t, int64(65), v.Int64)
		assert.False(t, v.Timestamp.IsZero(), "timestamp should be set")
	})

	t.Run("mixed cached and uncached", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		c.SetCachedValue(0, FieldGPUTemp, Value{
			FieldID: FieldGPUTemp,
			Status:  ValueStatusOK,
			Int64:   70,
		})

		vals, err := c.GetLatestValues(0, []FieldID{FieldGPUTemp, FieldPowerUsage})
		require.NoError(t, err)
		require.Len(t, vals, 2)

		assert.Equal(t, ValueStatusOK, vals[FieldGPUTemp].Status)
		assert.Equal(t, ValueStatusNotSupported, vals[FieldPowerUsage].Status)
	})
}

func TestClient_GetProfilingMetrics(t *testing.T) {
	t.Run("not connected returns error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		_, err := c.GetProfilingMetrics(0)
		require.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("placeholder returns profiling unavailable", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		_, err := c.GetProfilingMetrics(0)
		require.ErrorIs(t, err, ErrProfilingUnavailable)
	})
}

func TestClient_GetNVSwitchStatus(t *testing.T) {
	t.Run("not connected returns error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		_, err := c.GetNVSwitchStatus()
		require.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("placeholder returns not available", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		status, err := c.GetNVSwitchStatus()
		require.NoError(t, err)
		require.NotNil(t, status)
		assert.False(t, status.Available)
	})
}

func TestClient_SetHealthPolicy(t *testing.T) {
	t.Run("not connected returns error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		err := c.SetHealthPolicy(HealthPolicy{
			Name:       "test",
			Comparison: ComparisonGreaterThan,
		})
		require.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("invalid policy returns validation error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		err := c.SetHealthPolicy(HealthPolicy{
			Name:       "",
			Comparison: ComparisonGreaterThan,
		})
		require.ErrorIs(t, err, ErrHealthPolicyInvalid)
	})

	t.Run("invalid comparison returns validation error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		err := c.SetHealthPolicy(HealthPolicy{
			Name:       "test",
			Comparison: "invalid",
		})
		require.ErrorIs(t, err, ErrHealthPolicyInvalid)
	})

	t.Run("valid policy succeeds", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		err := c.SetHealthPolicy(HealthPolicy{
			Name:       "gpu-temp-high",
			Field:      FieldGPUTemp,
			Threshold:  85.0,
			Comparison: ComparisonGreaterThan,
			Enabled:    true,
			GPUID:      -1,
		})
		require.NoError(t, err)
	})
}

func TestClient_GetHealthViolations(t *testing.T) {
	t.Run("not connected returns error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		_, err := c.GetHealthViolations()
		require.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("placeholder returns empty slice", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		violations, err := c.GetHealthViolations()
		require.NoError(t, err)
		assert.Empty(t, violations)
	})
}

func TestClient_GetXIDErrors(t *testing.T) {
	t.Run("not connected returns error", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		_, err := c.GetXIDErrors(0, time.Now().Add(-time.Hour))
		require.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("placeholder returns empty slice", func(t *testing.T) {
		c := NewClient("localhost:5555", nil)
		require.NoError(t, c.Connect(context.Background()))

		errors, err := c.GetXIDErrors(0, time.Now().Add(-time.Hour))
		require.NoError(t, err)
		assert.Empty(t, errors)
	})
}

func TestClient_SetCachedValue(t *testing.T) {
	c := NewClient("localhost:5555", nil)

	// SetCachedValue works even without connection (for test setup)
	c.SetCachedValue(0, FieldGPUTemp, Value{
		FieldID: FieldGPUTemp,
		Status:  ValueStatusOK,
		Int64:   42,
	})

	c.SetCachedValue(0, FieldPowerUsage, Value{
		FieldID: FieldPowerUsage,
		Status:  ValueStatusOK,
		Int64:   150,
	})

	// Different GPU
	c.SetCachedValue(1, FieldGPUTemp, Value{
		FieldID: FieldGPUTemp,
		Status:  ValueStatusOK,
		Int64:   55,
	})

	assert.Equal(t, int64(42), c.cache[0][FieldGPUTemp].Int64)
	assert.Equal(t, int64(150), c.cache[0][FieldPowerUsage].Int64)
	assert.Equal(t, int64(55), c.cache[1][FieldGPUTemp].Int64)
}

func TestClient_Endpoint(t *testing.T) {
	c := NewClient("my-endpoint:1234", nil)
	assert.Equal(t, "my-endpoint:1234", c.Endpoint())
}

func TestClient_Lifecycle(t *testing.T) {
	c := NewClient("localhost:5555", nil)

	// Initial state
	assert.False(t, c.IsConnected())
	assert.True(t, c.IsPlaceholder())

	// Connect
	require.NoError(t, c.Connect(context.Background()))
	assert.True(t, c.IsConnected())

	// Watch fields
	require.NoError(t, c.WatchFields(0, DefaultWatchFields(), time.Second))

	// Get values (placeholder returns not-supported)
	vals, err := c.GetLatestValues(0, []FieldID{FieldGPUTemp})
	require.NoError(t, err)
	assert.Equal(t, ValueStatusNotSupported, vals[FieldGPUTemp].Status)

	// Disconnect
	require.NoError(t, c.Disconnect())
	assert.False(t, c.IsConnected())

	// Operations after disconnect fail
	_, err = c.GetLatestValues(0, []FieldID{FieldGPUTemp})
	require.ErrorIs(t, err, ErrNotInitialized)
}
