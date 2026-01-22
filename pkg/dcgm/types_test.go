// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"encoding/json"
	"testing"
	"time"
)

func TestValueStatus_String(t *testing.T) {
	tests := []struct {
		status ValueStatus
		want   string
	}{
		{ValueStatusOK, "ok"},
		{ValueStatusBlank, "blank"},
		{ValueStatusStale, "stale"},
		{ValueStatusNotFound, "not_found"},
		{ValueStatusNotSupported, "not_supported"},
		{ValueStatusError, "error"},
		{ValueStatus(99), "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValue_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		value Value
		want  bool
	}{
		{"OK", Value{Status: ValueStatusOK}, true},
		{"Blank", Value{Status: ValueStatusBlank}, false},
		{"Stale", Value{Status: ValueStatusStale}, false},
		{"NotFound", Value{Status: ValueStatusNotFound}, false},
		{"NotSupported", Value{Status: ValueStatusNotSupported}, false},
		{"Error", Value{Status: ValueStatusError}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValue_JSON(t *testing.T) {
	v := Value{
		FieldID:   FieldGPUTemp,
		Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		Status:    ValueStatusOK,
		Int64:     75,
	}

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Value
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.FieldID != v.FieldID {
		t.Errorf("FieldID = %v, want %v", decoded.FieldID, v.FieldID)
	}
	if decoded.Int64 != v.Int64 {
		t.Errorf("Int64 = %v, want %v", decoded.Int64, v.Int64)
	}
}

func TestProfilingMetrics_JSON(t *testing.T) {
	m := ProfilingMetrics{
		GPUID:          0,
		Timestamp:      time.Now(),
		SMOccupancy:    75.5,
		TensorActivity: 60.0,
		DRAMActivity:   45.2,
		PCIeTxBytes:    1000000,
		NVLinkTxBytes:  5000000,
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded ProfilingMetrics
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.SMOccupancy != m.SMOccupancy {
		t.Errorf("SMOccupancy = %v, want %v", decoded.SMOccupancy, m.SMOccupancy)
	}
}

func TestHealthPolicy_Validate(t *testing.T) {
	tests := []struct {
		name    string
		policy  HealthPolicy
		wantErr bool
	}{
		{
			name: "valid-gt",
			policy: HealthPolicy{
				Name:       "temp-high",
				Field:      FieldGPUTemp,
				Threshold:  85.0,
				Comparison: ComparisonGreaterThan,
			},
			wantErr: false,
		},
		{
			name: "valid-lt",
			policy: HealthPolicy{
				Name:       "temp-low",
				Field:      FieldGPUTemp,
				Threshold:  20.0,
				Comparison: ComparisonLessThan,
			},
			wantErr: false,
		},
		{
			name: "valid-eq",
			policy: HealthPolicy{
				Name:       "exact",
				Field:      FieldGPUUtil,
				Threshold:  100.0,
				Comparison: ComparisonEqual,
			},
			wantErr: false,
		},
		{
			name: "empty-name",
			policy: HealthPolicy{
				Name:       "",
				Comparison: ComparisonGreaterThan,
			},
			wantErr: true,
		},
		{
			name: "invalid-comparison",
			policy: HealthPolicy{
				Name:       "test",
				Comparison: "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "disabled",
			config:  Config{Enabled: false},
			wantErr: false,
		},
		{
			name: "valid-embedded",
			config: Config{
				Enabled:       true,
				Mode:          "embedded",
				EmbeddedPort:  5555,
				WatchInterval: time.Second,
			},
			wantErr: false,
		},
		{
			name: "valid-external",
			config: Config{
				Enabled:       true,
				Mode:          "external",
				Socket:        "/var/run/dcgm.sock",
				WatchInterval: time.Second,
			},
			wantErr: false,
		},
		{
			name: "invalid-mode",
			config: Config{
				Enabled: true,
				Mode:    "invalid",
			},
			wantErr: true,
		},
		{
			name: "external-missing-socket",
			config: Config{
				Enabled:       true,
				Mode:          "external",
				Socket:        "",
				WatchInterval: time.Second,
			},
			wantErr: true,
		},
		{
			name: "interval-too-short",
			config: Config{
				Enabled:       true,
				Mode:          "embedded",
				EmbeddedPort:  5555,
				WatchInterval: 10 * time.Millisecond,
			},
			wantErr: true,
		},
		{
			name: "invalid-port",
			config: Config{
				Enabled:       true,
				Mode:          "embedded",
				EmbeddedPort:  0,
				WatchInterval: time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Enabled {
		t.Error("default should be disabled")
	}
	if cfg.Mode != "embedded" {
		t.Errorf("Mode = %s, want embedded", cfg.Mode)
	}
	if cfg.Socket != "/var/run/dcgm.sock" {
		t.Errorf("Socket = %s, want /var/run/dcgm.sock", cfg.Socket)
	}
	if cfg.EmbeddedPort != 5555 {
		t.Errorf("EmbeddedPort = %d, want 5555", cfg.EmbeddedPort)
	}
	if cfg.WatchInterval != time.Second {
		t.Errorf("WatchInterval = %v, want 1s", cfg.WatchInterval)
	}
	if len(cfg.Fields) == 0 {
		t.Error("Fields should not be empty")
	}
}

func TestDefaultWatchFields(t *testing.T) {
	fields := DefaultWatchFields()
	if len(fields) == 0 {
		t.Error("DefaultWatchFields should return non-empty slice")
	}

	// Verify expected fields
	expected := map[FieldID]bool{
		FieldGPUTemp:    true,
		FieldPowerUsage: true,
		FieldGPUUtil:    true,
		FieldMemUtil:    true,
		FieldMemUsed:    true,
		FieldXIDErrors:  true,
	}

	for _, f := range fields {
		if !expected[f] {
			t.Errorf("unexpected field %d in DefaultWatchFields", f)
		}
	}
}

func TestProfilingWatchFields(t *testing.T) {
	fields := ProfilingWatchFields()
	if len(fields) == 0 {
		t.Error("ProfilingWatchFields should return non-empty slice")
	}

	// Verify expected profiling fields
	expectedFields := []FieldID{
		FieldSMActive,
		FieldSMOccupancy,
		FieldTensorActive,
		FieldDRAMActive,
	}

	fieldSet := make(map[FieldID]bool)
	for _, f := range fields {
		fieldSet[f] = true
	}

	for _, expected := range expectedFields {
		if !fieldSet[expected] {
			t.Errorf("expected field %d not in ProfilingWatchFields", expected)
		}
	}
}

func TestNVSwitchStatus_JSON(t *testing.T) {
	status := NVSwitchStatus{
		Available:   true,
		SwitchCount: 2,
		Switches: []NVSwitchInfo{
			{ID: 0, Status: SwitchHealthHealthy, Temperature: 45},
			{ID: 1, Status: SwitchHealthHealthy, Temperature: 46},
		},
		Links: []NVSwitchLink{
			{SwitchID: 0, LinkID: 0, RemoteGPU: 0, State: LinkStateActive},
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded NVSwitchStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.SwitchCount != status.SwitchCount {
		t.Errorf("SwitchCount = %v, want %v", decoded.SwitchCount, status.SwitchCount)
	}
}
