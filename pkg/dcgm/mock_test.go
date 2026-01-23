// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMock_Interface(t *testing.T) {
	// Verify Mock implements Interface
	var _ Interface = (*Mock)(nil)
}

func TestMock_NewMock(t *testing.T) {
	tests := []struct {
		name     string
		gpuCount int
		want     int
	}{
		{"default", 0, 2},
		{"negative", -1, 2},
		{"one", 1, 1},
		{"four", 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMock(tt.gpuCount)
			if mock == nil {
				t.Fatal("NewMock returned nil")
			}
			if mock.GPUCount() != tt.want {
				t.Errorf("GPUCount() = %d, want %d", mock.GPUCount(), tt.want)
			}
		})
	}
}

func TestMock_Init_Shutdown(t *testing.T) {
	mock := NewMock(2)

	// Init should succeed
	err := mock.Init(context.Background())
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !mock.IsAvailable() {
		t.Error("IsAvailable should return true after Init")
	}

	// Double init should fail
	err = mock.Init(context.Background())
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Errorf("second Init should return ErrAlreadyInitialized, got %v", err)
	}

	// Shutdown should succeed
	err = mock.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	if mock.IsAvailable() {
		t.Error("IsAvailable should return false after Shutdown")
	}
}

func TestMock_Init_Unavailable(t *testing.T) {
	mock := NewMock(2)
	mock.SetAvailable(false)

	err := mock.Init(context.Background())
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("Init should return ErrDCGMUnavailable when unavailable, got %v", err)
	}
}

func TestMock_WatchFields(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	// Valid call
	err := mock.WatchFields(0, DefaultWatchFields(), time.Second)
	if err != nil {
		t.Errorf("WatchFields failed: %v", err)
	}

	// Verify fields are recorded
	watched := mock.WatchedFields(0)
	if len(watched) != len(DefaultWatchFields()) {
		t.Errorf("WatchedFields count = %d, want %d", len(watched), len(DefaultWatchFields()))
	}

	// Invalid GPU
	err = mock.WatchFields(99, DefaultWatchFields(), time.Second)
	if !errors.Is(err, ErrInvalidGPU) {
		t.Errorf("WatchFields with invalid GPU should return ErrInvalidGPU, got %v", err)
	}

	// Interval too short
	err = mock.WatchFields(0, DefaultWatchFields(), 10*time.Millisecond)
	if !errors.Is(err, ErrIntervalTooShort) {
		t.Errorf("WatchFields with short interval should return ErrIntervalTooShort, got %v", err)
	}
}

func TestMock_WatchFields_NotInitialized(t *testing.T) {
	mock := NewMock(2)
	err := mock.WatchFields(0, DefaultWatchFields(), time.Second)
	if !errors.Is(err, ErrNotInitialized) {
		t.Errorf("WatchFields without Init should return ErrNotInitialized, got %v", err)
	}
}

func TestMock_GetLatestValues(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	fields := []FieldID{FieldGPUTemp, FieldPowerUsage, FieldGPUUtil}
	values, err := mock.GetLatestValues(0, fields)
	if err != nil {
		t.Fatalf("GetLatestValues failed: %v", err)
	}

	if len(values) != len(fields) {
		t.Errorf("values count = %d, want %d", len(values), len(fields))
	}

	// Check GPU temp value
	if temp, ok := values[FieldGPUTemp]; ok {
		if temp.Status != ValueStatusOK {
			t.Errorf("FieldGPUTemp status = %v, want OK", temp.Status)
		}
		if temp.Int64 <= 0 {
			t.Errorf("FieldGPUTemp value = %d, want > 0", temp.Int64)
		}
	} else {
		t.Error("FieldGPUTemp not found in values")
	}

	// Request unsupported field
	unsupported := []FieldID{FieldID(99999)}
	values, err = mock.GetLatestValues(0, unsupported)
	if err != nil {
		t.Fatalf("GetLatestValues failed: %v", err)
	}
	if values[FieldID(99999)].Status != ValueStatusNotSupported {
		t.Error("unsupported field should have NotSupported status")
	}
}

func TestMock_GetLatestValues_InvalidGPU(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	_, err := mock.GetLatestValues(99, DefaultWatchFields())
	if !errors.Is(err, ErrInvalidGPU) {
		t.Errorf("GetLatestValues with invalid GPU should return ErrInvalidGPU, got %v", err)
	}
}

func TestMock_GetProfilingMetrics(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	metrics, err := mock.GetProfilingMetrics(0)
	if err != nil {
		t.Fatalf("GetProfilingMetrics failed: %v", err)
	}

	if metrics.GPUID != 0 {
		t.Errorf("GPUID = %d, want 0", metrics.GPUID)
	}

	if metrics.SMOccupancy <= 0 {
		t.Errorf("SMOccupancy = %f, want > 0", metrics.SMOccupancy)
	}
}

func TestMock_GetProfilingMetrics_InvalidGPU(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	_, err := mock.GetProfilingMetrics(99)
	if !errors.Is(err, ErrInvalidGPU) {
		t.Errorf("GetProfilingMetrics with invalid GPU should return ErrInvalidGPU, got %v", err)
	}
}

func TestMock_GetNVSwitchStatus_Default(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	status, err := mock.GetNVSwitchStatus()
	if err != nil {
		t.Fatalf("GetNVSwitchStatus failed: %v", err)
	}

	if status.Available {
		t.Error("default NVSwitch status should be unavailable")
	}
}

func TestMock_GetNVSwitchStatus_Configured(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	mock.SetNVSwitchStatus(&NVSwitchStatus{
		Available:   true,
		SwitchCount: 2,
	})

	status, err := mock.GetNVSwitchStatus()
	if err != nil {
		t.Fatalf("GetNVSwitchStatus failed: %v", err)
	}

	if !status.Available {
		t.Error("configured NVSwitch status should be available")
	}
	if status.SwitchCount != 2 {
		t.Errorf("SwitchCount = %d, want 2", status.SwitchCount)
	}
}

func TestMock_HealthPolicy(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	policy := HealthPolicy{
		Name:       "temp-high",
		Field:      FieldGPUTemp,
		Threshold:  85.0,
		Comparison: ComparisonGreaterThan,
		Enabled:    true,
		GPUID:      -1,
	}

	err := mock.SetHealthPolicy(policy)
	if err != nil {
		t.Fatalf("SetHealthPolicy failed: %v", err)
	}

	// Invalid policy
	invalid := HealthPolicy{
		Name:       "",
		Comparison: ComparisonGreaterThan,
	}
	err = mock.SetHealthPolicy(invalid)
	if err == nil {
		t.Error("SetHealthPolicy should fail with empty name")
	}
}

func TestMock_HealthViolations(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	// Initially empty
	violations, err := mock.GetHealthViolations()
	if err != nil {
		t.Fatalf("GetHealthViolations failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("violations count = %d, want 0", len(violations))
	}

	// Add violation
	mock.AddHealthViolation(HealthViolation{
		Policy:      "temp-high",
		GPUID:       0,
		Timestamp:   time.Now(),
		ActualValue: 90.0,
		Threshold:   85.0,
		Message:     "GPU temperature exceeded threshold",
	})

	violations, err = mock.GetHealthViolations()
	if err != nil {
		t.Fatalf("GetHealthViolations failed: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("violations count = %d, want 1", len(violations))
	}

	// Clear violations
	mock.ClearHealthViolations()
	violations, _ = mock.GetHealthViolations()
	if len(violations) != 0 {
		t.Error("violations should be empty after clear")
	}
}

func TestMock_XIDErrors(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	past := time.Now().Add(-time.Hour)
	now := time.Now()

	// Initially empty
	errors, err := mock.GetXIDErrors(0, past)
	if err != nil {
		t.Fatalf("GetXIDErrors failed: %v", err)
	}
	if len(errors) != 0 {
		t.Errorf("errors count = %d, want 0", len(errors))
	}

	// Add XID error
	mock.AddXIDError(0, XIDError{
		Timestamp: now,
		GPUID:     0,
		Code:      31,
		Count:     1,
	})

	// Query with past time
	errors, err = mock.GetXIDErrors(0, past)
	if err != nil {
		t.Fatalf("GetXIDErrors failed: %v", err)
	}
	if len(errors) != 1 {
		t.Errorf("errors count = %d, want 1", len(errors))
	}

	// Query with future time
	future := time.Now().Add(time.Hour)
	errors, err = mock.GetXIDErrors(0, future)
	if err != nil {
		t.Fatalf("GetXIDErrors failed: %v", err)
	}
	if len(errors) != 0 {
		t.Errorf("errors count = %d, want 0 for future time", len(errors))
	}

	// Clear errors
	mock.ClearXIDErrors(0)
	errors, _ = mock.GetXIDErrors(0, past)
	if len(errors) != 0 {
		t.Error("errors should be empty after clear")
	}
}

func TestMock_XIDErrors_InvalidGPU(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	_, err := mock.GetXIDErrors(99, time.Now())
	if !errors.Is(err, ErrInvalidGPU) {
		t.Errorf("GetXIDErrors with invalid GPU should return ErrInvalidGPU, got %v", err)
	}
}

func TestMock_SetFieldValue(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	// Set custom value
	customValue := Value{
		FieldID:   FieldGPUTemp,
		Timestamp: time.Now(),
		Status:    ValueStatusOK,
		Int64:     99,
	}
	mock.SetFieldValue(0, FieldGPUTemp, customValue)

	// Verify custom value is returned
	values, err := mock.GetLatestValues(0, []FieldID{FieldGPUTemp})
	if err != nil {
		t.Fatalf("GetLatestValues failed: %v", err)
	}

	if values[FieldGPUTemp].Int64 != 99 {
		t.Errorf("FieldGPUTemp value = %d, want 99", values[FieldGPUTemp].Int64)
	}
}

func TestMock_SetProfilingData(t *testing.T) {
	mock := NewMock(2)
	_ = mock.Init(context.Background())

	custom := &ProfilingMetrics{
		GPUID:          0,
		SMOccupancy:    99.9,
		TensorActivity: 88.8,
	}
	mock.SetProfilingData(0, custom)

	metrics, err := mock.GetProfilingMetrics(0)
	if err != nil {
		t.Fatalf("GetProfilingMetrics failed: %v", err)
	}

	if metrics.SMOccupancy != 99.9 {
		t.Errorf("SMOccupancy = %f, want 99.9", metrics.SMOccupancy)
	}
}
