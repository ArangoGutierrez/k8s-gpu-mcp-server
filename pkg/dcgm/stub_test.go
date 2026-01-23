// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStub_Interface(t *testing.T) {
	// Verify Stub implements Interface
	var _ Interface = (*Stub)(nil)
}

func TestStub_NewStub(t *testing.T) {
	stub := NewStub()
	if stub == nil {
		t.Fatal("NewStub returned nil")
	}
}

func TestStub_Init(t *testing.T) {
	stub := NewStub()
	err := stub.Init(context.Background())
	if err != nil {
		t.Errorf("Init should return nil, got %v", err)
	}
}

func TestStub_Shutdown(t *testing.T) {
	stub := NewStub()
	err := stub.Shutdown(context.Background())
	if err != nil {
		t.Errorf("Shutdown should return nil, got %v", err)
	}
}

func TestStub_IsAvailable(t *testing.T) {
	stub := NewStub()
	if stub.IsAvailable() {
		t.Error("IsAvailable should return false for stub")
	}
}

func TestStub_WatchFields(t *testing.T) {
	stub := NewStub()
	err := stub.WatchFields(0, DefaultWatchFields(), time.Second)
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("WatchFields should return ErrDCGMUnavailable, got %v", err)
	}
}

func TestStub_GetLatestValues(t *testing.T) {
	stub := NewStub()
	values, err := stub.GetLatestValues(0, DefaultWatchFields())
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("GetLatestValues should return ErrDCGMUnavailable, got %v", err)
	}
	if values != nil {
		t.Error("GetLatestValues should return nil values")
	}
}

func TestStub_GetProfilingMetrics(t *testing.T) {
	stub := NewStub()
	metrics, err := stub.GetProfilingMetrics(0)
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("GetProfilingMetrics should return ErrDCGMUnavailable, got %v", err)
	}
	if metrics != nil {
		t.Error("GetProfilingMetrics should return nil metrics")
	}
}

func TestStub_GetNVSwitchStatus(t *testing.T) {
	stub := NewStub()
	status, err := stub.GetNVSwitchStatus()
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("GetNVSwitchStatus should return ErrDCGMUnavailable, got %v", err)
	}
	if status != nil {
		t.Error("GetNVSwitchStatus should return nil status")
	}
}

func TestStub_SetHealthPolicy(t *testing.T) {
	stub := NewStub()
	err := stub.SetHealthPolicy(HealthPolicy{Name: "test"})
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("SetHealthPolicy should return ErrDCGMUnavailable, got %v", err)
	}
}

func TestStub_GetHealthViolations(t *testing.T) {
	stub := NewStub()
	violations, err := stub.GetHealthViolations()
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("GetHealthViolations should return ErrDCGMUnavailable, got %v", err)
	}
	if violations != nil {
		t.Error("GetHealthViolations should return nil violations")
	}
}

func TestStub_GetXIDErrors(t *testing.T) {
	stub := NewStub()
	xidErrs, err := stub.GetXIDErrors(0, time.Now())
	if !errors.Is(err, ErrDCGMUnavailable) {
		t.Errorf("GetXIDErrors should return ErrDCGMUnavailable, got %v", err)
	}
	if xidErrs != nil {
		t.Error("GetXIDErrors should return nil errors")
	}
}
