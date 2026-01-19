// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGPUHealthHandler(t *testing.T) {
	mockClient := nvml.NewMock(2)
	handler := NewGPUHealthHandler(mockClient)

	require.NotNil(t, handler)
	assert.NotNil(t, handler.nvmlClient)
}

func TestGPUHealthHandler_Handle_HealthyGPU(t *testing.T) {
	// Use custom mock with values that result in healthy status
	mockClient := &mockHealthyNVML{}
	handler := NewGPUHealthHandler(mockClient)
	ctx := context.Background()

	request := mcp.CallToolRequest{}
	result, err := handler.Handle(ctx, request)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	// Parse response
	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok, "content should be TextContent")

	var response GPUHealthResponse
	err = json.Unmarshal([]byte(textContent.Text), &response)
	require.NoError(t, err)

	assert.Equal(t, "healthy", response.Status)
	assert.GreaterOrEqual(t, response.OverallScore, 90)
	assert.Equal(t, 1, response.DeviceCount)
	assert.Equal(t, 1, response.HealthyCount)
	assert.Equal(t, 0, response.DegradedCount)
	assert.Equal(t, 0, response.CriticalCount)
	assert.Len(t, response.GPUs, 1)
	assert.Contains(t, response.Recommendation, "healthy")

	// Check GPU details
	gpu := response.GPUs[0]
	assert.Equal(t, 0, gpu.Index)
	assert.Contains(t, gpu.Name, "Tesla T4")
	assert.NotEmpty(t, gpu.UUID)
	assert.NotEmpty(t, gpu.PCIBusID)
	assert.Equal(t, "healthy", gpu.Status)
	assert.GreaterOrEqual(t, gpu.HealthScore, 90)
	assert.Equal(t, "normal", gpu.Temperature.Status)
	assert.Equal(t, "normal", gpu.Memory.Status)
	assert.Equal(t, "normal", gpu.Power.Status)
	assert.Empty(t, gpu.Issues)
}

func TestGPUHealthHandler_Handle_MultipleGPUs(t *testing.T) {
	mockClient := nvml.NewMock(4)
	handler := NewGPUHealthHandler(mockClient)
	ctx := context.Background()

	request := mcp.CallToolRequest{}
	result, err := handler.Handle(ctx, request)

	require.NoError(t, err)
	require.NotNil(t, result)

	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)

	var response GPUHealthResponse
	err = json.Unmarshal([]byte(textContent.Text), &response)
	require.NoError(t, err)

	assert.Equal(t, 4, response.DeviceCount)
	assert.Len(t, response.GPUs, 4)

	// Verify each GPU has unique index
	for i, gpu := range response.GPUs {
		assert.Equal(t, i, gpu.Index)
	}
}

func TestGPUHealthHandler_Handle_NoDevices(t *testing.T) {
	// Use custom mock that returns 0 devices
	mockClient := &mockEmptyNVML{}
	handler := NewGPUHealthHandler(mockClient)
	ctx := context.Background()

	request := mcp.CallToolRequest{}
	result, err := handler.Handle(ctx, request)

	require.NoError(t, err)
	require.NotNil(t, result)

	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)

	var response GPUHealthResponse
	err = json.Unmarshal([]byte(textContent.Text), &response)
	require.NoError(t, err)

	assert.Equal(t, "unknown", response.Status)
	assert.Equal(t, 0, response.OverallScore)
	assert.Equal(t, 0, response.DeviceCount)
	assert.Empty(t, response.GPUs)
	assert.Contains(t, response.Recommendation, "No GPU devices")
}

func TestGPUHealthHandler_Handle_ContextCancellation(t *testing.T) {
	mockClient := nvml.NewMock(10)
	handler := NewGPUHealthHandler(mockClient)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	request := mcp.CallToolRequest{}
	result, err := handler.Handle(ctx, request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Should return error result
	assert.True(t, result.IsError)
	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "cancelled")
}

func TestGPUHealthHandler_checkTemperature(t *testing.T) {
	handler := &GPUHealthHandler{}

	tests := []struct {
		name       string
		temp       uint32
		wantStatus string
		wantMargin int
	}{
		{
			name:       "normal_low",
			temp:       30,
			wantStatus: "normal",
			wantMargin: 52,
		},
		{
			name:       "normal_mid",
			temp:       60,
			wantStatus: "normal",
			wantMargin: 22,
		},
		{
			name:       "elevated",
			temp:       75,
			wantStatus: "elevated",
			wantMargin: 7,
		},
		{
			name:       "high",
			temp:       85,
			wantStatus: "high",
			wantMargin: -3,
		},
		{
			name:       "critical",
			temp:       92,
			wantStatus: "critical",
			wantMargin: -10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock device with specific temperature
			mockDevice := &mockDeviceWithTemp{temp: tt.temp}

			result := handler.checkTemperature(context.Background(), mockDevice)

			assert.Equal(t, tt.temp, result.Current)
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, tt.wantMargin, result.Margin)
			assert.Equal(t, uint32(82), result.Threshold)
			assert.Equal(t, uint32(90), result.Max)
		})
	}
}

func TestGPUHealthHandler_checkMemory(t *testing.T) {
	handler := &GPUHealthHandler{}

	tests := []struct {
		name        string
		total       uint64
		used        uint64
		wantStatus  string
		wantPercent float64
	}{
		{
			name:        "normal_low",
			total:       16000000000,
			used:        1600000000,
			wantStatus:  "normal",
			wantPercent: 10.0,
		},
		{
			name:        "normal_high",
			total:       16000000000,
			used:        12000000000,
			wantStatus:  "normal",
			wantPercent: 75.0,
		},
		{
			name:        "elevated",
			total:       16000000000,
			used:        13600000000,
			wantStatus:  "elevated",
			wantPercent: 85.0,
		},
		{
			name:        "high",
			total:       16000000000,
			used:        14560000000,
			wantStatus:  "high",
			wantPercent: 91.0,
		},
		{
			name:        "critical",
			total:       16000000000,
			used:        15520000000,
			wantStatus:  "critical",
			wantPercent: 97.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDevice := &mockDeviceWithMemory{
				total: tt.total,
				used:  tt.used,
			}

			result := handler.checkMemory(context.Background(), mockDevice)

			assert.Equal(t, tt.total, result.Total)
			assert.Equal(t, tt.used, result.Used)
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.InDelta(t, tt.wantPercent, result.UsedPercent, 1.0)
		})
	}
}

func TestGPUHealthHandler_checkPower(t *testing.T) {
	handler := &GPUHealthHandler{}

	tests := []struct {
		name       string
		power      uint32
		wantStatus string
	}{
		{
			name:       "normal_low",
			power:      14000, // 20% of 70000
			wantStatus: "normal",
		},
		{
			name:       "normal_high",
			power:      49000, // 70% of 70000
			wantStatus: "normal",
		},
		{
			name:       "elevated",
			power:      59500, // 85% of 70000
			wantStatus: "elevated",
		},
		{
			name:       "high",
			power:      68600, // 98% of 70000
			wantStatus: "high",
		},
		{
			name:       "over_limit",
			power:      77000, // 110% of 70000
			wantStatus: "over_limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDevice := &mockDeviceWithPower{power: tt.power}

			result := handler.checkPower(context.Background(), mockDevice)

			assert.Equal(t, tt.power, result.Current)
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, uint32(70000), result.Limit)
		})
	}
}

func TestGPUHealthHandler_calculateHealthScore(t *testing.T) {
	handler := &GPUHealthHandler{}

	tests := []struct {
		name      string
		health    GPUHealthStatus
		wantScore int
		wantMin   int
		wantMax   int
	}{
		{
			name: "perfect_health",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "normal"},
				Memory:      MemoryHealth{Status: "normal"},
				Power:       PowerHealth{Status: "normal"},
				Throttling:  ThrottlingStatus{Status: "none"},
				ECCErrors:   ECCHealth{Status: "healthy"},
				Issues:      []HealthIssue{},
			},
			wantScore: 100,
		},
		{
			name: "elevated_temp",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "elevated", Current: 75},
				Memory:      MemoryHealth{Status: "normal"},
				Power:       PowerHealth{Status: "normal"},
				Throttling:  ThrottlingStatus{Status: "none"},
				ECCErrors:   ECCHealth{Status: "healthy"},
				Issues:      []HealthIssue{},
			},
			wantScore: 90,
		},
		{
			name: "high_temp",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "high", Current: 85},
				Memory:      MemoryHealth{Status: "normal"},
				Power:       PowerHealth{Status: "normal"},
				Throttling:  ThrottlingStatus{Status: "none"},
				ECCErrors:   ECCHealth{Status: "healthy"},
				Issues:      []HealthIssue{},
			},
			wantScore: 80,
		},
		{
			name: "critical_temp",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "critical", Current: 92},
				Memory:      MemoryHealth{Status: "normal"},
				Power:       PowerHealth{Status: "normal"},
				Throttling:  ThrottlingStatus{Status: "none"},
				ECCErrors:   ECCHealth{Status: "healthy"},
				Issues:      []HealthIssue{},
			},
			wantScore: 70,
		},
		{
			name: "critical_memory",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "normal"},
				Memory:      MemoryHealth{Status: "critical", UsedPercent: 97},
				Power:       PowerHealth{Status: "normal"},
				Throttling:  ThrottlingStatus{Status: "none"},
				ECCErrors:   ECCHealth{Status: "healthy"},
				Issues:      []HealthIssue{},
			},
			wantScore: 80,
		},
		{
			name: "uncorrectable_ecc",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "normal"},
				Memory:      MemoryHealth{Status: "normal"},
				Power:       PowerHealth{Status: "normal"},
				Throttling:  ThrottlingStatus{Status: "none"},
				ECCErrors:   ECCHealth{TotalUncorrectableErrors: 1},
				Issues:      []HealthIssue{},
			},
			wantScore: 70,
		},
		{
			name: "severe_throttling",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "normal"},
				Memory:      MemoryHealth{Status: "normal"},
				Power:       PowerHealth{Status: "normal"},
				Throttling:  ThrottlingStatus{Status: "severe"},
				ECCErrors:   ECCHealth{Status: "healthy"},
				Issues:      []HealthIssue{},
			},
			wantScore: 75,
		},
		{
			name: "multiple_issues",
			health: GPUHealthStatus{
				Temperature: TemperatureHealth{Status: "high", Current: 85},
				Memory:      MemoryHealth{Status: "high", UsedPercent: 92},
				Power:       PowerHealth{Status: "high"},
				Throttling:  ThrottlingStatus{Status: "none"},
				ECCErrors:   ECCHealth{Status: "healthy"},
				Issues:      []HealthIssue{},
			},
			wantMin: 50,
			wantMax: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Need to pass pointer since issues are appended
			health := tt.health
			score := handler.calculateHealthScore(&health)

			if tt.wantScore > 0 {
				assert.Equal(t, tt.wantScore, score)
			} else {
				assert.GreaterOrEqual(t, score, tt.wantMin)
				assert.LessOrEqual(t, score, tt.wantMax)
			}
		})
	}
}

func TestGPUHealthHandler_determineStatus(t *testing.T) {
	handler := &GPUHealthHandler{}

	tests := []struct {
		name   string
		score  int
		issues []HealthIssue
		want   string
	}{
		{
			name:   "healthy_high_score",
			score:  95,
			issues: []HealthIssue{},
			want:   "healthy",
		},
		{
			name:   "healthy_threshold",
			score:  90,
			issues: []HealthIssue{},
			want:   "healthy",
		},
		{
			name:   "warning",
			score:  75,
			issues: []HealthIssue{},
			want:   "warning",
		},
		{
			name:   "degraded",
			score:  55,
			issues: []HealthIssue{},
			want:   "degraded",
		},
		{
			name:   "critical_score",
			score:  40,
			issues: []HealthIssue{},
			want:   "critical",
		},
		{
			name:  "critical_issue_overrides_score",
			score: 95,
			issues: []HealthIssue{
				{Severity: "critical", Component: "ecc"},
			},
			want: "critical",
		},
		{
			name:  "warning_issue_no_override",
			score: 95,
			issues: []HealthIssue{
				{Severity: "warning", Component: "memory"},
			},
			want: "healthy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := handler.determineStatus(tt.score, tt.issues)
			assert.Equal(t, tt.want, status)
		})
	}
}

func TestGPUHealthHandler_calculateOverallHealth(t *testing.T) {
	handler := &GPUHealthHandler{}

	tests := []struct {
		name       string
		gpus       []GPUHealthStatus
		wantStatus string
		wantScore  int
	}{
		{
			name:       "no_gpus",
			gpus:       []GPUHealthStatus{},
			wantStatus: "unknown",
			wantScore:  0,
		},
		{
			name: "all_healthy",
			gpus: []GPUHealthStatus{
				{Status: "healthy", HealthScore: 95},
				{Status: "healthy", HealthScore: 92},
			},
			wantStatus: "healthy",
			wantScore:  92, // Worst score
		},
		{
			name: "one_degraded",
			gpus: []GPUHealthStatus{
				{Status: "healthy", HealthScore: 95},
				{Status: "degraded", HealthScore: 55},
			},
			wantStatus: "degraded",
			wantScore:  55,
		},
		{
			name: "one_critical",
			gpus: []GPUHealthStatus{
				{Status: "healthy", HealthScore: 95},
				{Status: "critical", HealthScore: 30},
				{Status: "healthy", HealthScore: 90},
			},
			wantStatus: "critical",
			wantScore:  30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.calculateOverallHealth(tt.gpus)

			assert.Equal(t, tt.wantStatus, response.Status)
			assert.Equal(t, tt.wantScore, response.OverallScore)
			assert.Equal(t, len(tt.gpus), response.DeviceCount)
		})
	}
}

func TestGPUHealthHandler_generateRecommendation(t *testing.T) {
	handler := &GPUHealthHandler{}

	tests := []struct {
		name         string
		response     GPUHealthResponse
		wantContains []string
	}{
		{
			name: "no_devices",
			response: GPUHealthResponse{
				DeviceCount: 0,
			},
			wantContains: []string{"No GPU devices"},
		},
		{
			name: "all_healthy",
			response: GPUHealthResponse{
				DeviceCount:  2,
				HealthyCount: 2,
			},
			wantContains: []string{"healthy", "No action"},
		},
		{
			name: "some_degraded",
			response: GPUHealthResponse{
				DeviceCount:   2,
				HealthyCount:  1,
				DegradedCount: 1,
			},
			wantContains: []string{"degraded", "Monitor"},
		},
		{
			name: "critical",
			response: GPUHealthResponse{
				DeviceCount:   2,
				HealthyCount:  1,
				CriticalCount: 1,
			},
			wantContains: []string{"critical", "Immediate"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := handler.generateRecommendation(tt.response)

			for _, substr := range tt.wantContains {
				assert.Contains(t, rec, substr)
			}
		})
	}
}

func TestGetGPUHealthTool(t *testing.T) {
	tool := GetGPUHealthTool()

	assert.Equal(t, "get_gpu_health", tool.Name)
	assert.NotEmpty(t, tool.Description)
	assert.Contains(t, tool.Description, "health")
	assert.Contains(t, tool.Description, "temperature")
	assert.Contains(t, tool.Description, "score")
}

func TestGPUHealthResponse_JSONSerialization(t *testing.T) {
	response := GPUHealthResponse{
		Status:         "healthy",
		OverallScore:   95,
		DeviceCount:    1,
		HealthyCount:   1,
		DegradedCount:  0,
		CriticalCount:  0,
		Recommendation: "All GPUs healthy",
		GPUs: []GPUHealthStatus{
			{
				Index:       0,
				Name:        "Tesla T4",
				UUID:        "GPU-12345",
				PCIBusID:    "0000:00:1E.0",
				Status:      "healthy",
				HealthScore: 95,
				Temperature: TemperatureHealth{
					Current:   35,
					Threshold: 82,
					Max:       90,
					Status:    "normal",
					Margin:    47,
				},
				Memory: MemoryHealth{
					Total:       16000000000,
					Used:        1600000000,
					Free:        14400000000,
					UsedPercent: 10.0,
					Status:      "normal",
				},
				Power: PowerHealth{
					Current:     14000,
					Limit:       70000,
					Default:     70000,
					UsedPercent: 20.0,
					Status:      "normal",
				},
				Throttling: ThrottlingStatus{
					Active:  false,
					Reasons: []string{},
					Status:  "none",
				},
				ECCErrors: ECCHealth{
					Enabled:                  true,
					TotalCorrectableErrors:   0,
					TotalUncorrectableErrors: 0,
					Status:                   "healthy",
				},
				Performance: PerformanceHealth{
					GPUUtil:    30,
					MemoryUtil: 20,
					Status:     "idle",
				},
			},
		},
	}

	jsonBytes, err := json.Marshal(response)
	require.NoError(t, err)

	var decoded GPUHealthResponse
	err = json.Unmarshal(jsonBytes, &decoded)
	require.NoError(t, err)

	assert.Equal(t, response.Status, decoded.Status)
	assert.Equal(t, response.OverallScore, decoded.OverallScore)
	assert.Equal(t, response.DeviceCount, decoded.DeviceCount)
	assert.Len(t, decoded.GPUs, 1)
	assert.Equal(t, response.GPUs[0].Name, decoded.GPUs[0].Name)
	assert.Equal(t, response.GPUs[0].Temperature.Current,
		decoded.GPUs[0].Temperature.Current)
}

func TestGPUHealthStatus_IssuesOmittedWhenEmpty(t *testing.T) {
	status := GPUHealthStatus{
		Index:  0,
		Status: "healthy",
		Issues: []HealthIssue{}, // Empty
	}

	jsonBytes, err := json.Marshal(status)
	require.NoError(t, err)

	// Issues should be omitted from JSON when empty
	assert.NotContains(t, string(jsonBytes), "issues")
}

// Mock devices for specific test scenarios

type mockDeviceWithTemp struct {
	nvml.Device
	temp uint32
}

func (d *mockDeviceWithTemp) GetTemperature(ctx context.Context) (uint32, error) {
	return d.temp, nil
}

func (d *mockDeviceWithTemp) GetName(ctx context.Context) (string, error) {
	return "Mock GPU", nil
}

func (d *mockDeviceWithTemp) GetUUID(ctx context.Context) (string, error) {
	return "GPU-MOCK-0001", nil
}

func (d *mockDeviceWithTemp) GetPCIInfo(ctx context.Context) (*nvml.PCIInfo, error) {
	return &nvml.PCIInfo{BusID: "0000:01:00.0"}, nil
}

func (d *mockDeviceWithTemp) GetMemoryInfo(ctx context.Context) (*nvml.MemoryInfo, error) {
	return &nvml.MemoryInfo{Total: 16000000000, Used: 1000000000, Free: 15000000000}, nil
}

func (d *mockDeviceWithTemp) GetPowerUsage(ctx context.Context) (uint32, error) {
	return 14000, nil
}

func (d *mockDeviceWithTemp) GetUtilizationRates(ctx context.Context) (*nvml.Utilization, error) {
	return &nvml.Utilization{GPU: 30, Memory: 20}, nil
}
func (d *mockDeviceWithTemp) GetPowerManagementLimit(ctx context.Context) (uint32, error) {
	return 70000, nil
}
func (d *mockDeviceWithTemp) GetEccMode(ctx context.Context) (bool, bool, error) {
	return false, false, nil
}
func (d *mockDeviceWithTemp) GetTotalEccErrors(ctx context.Context, _ int) (uint64, error) {
	return 0, nil
}
func (d *mockDeviceWithTemp) GetCurrentClocksThrottleReasons(ctx context.Context) (uint64, error) {
	return 0, nil
}
func (d *mockDeviceWithTemp) GetClockInfo(ctx context.Context, clockType int) (uint32, error) {
	if clockType == nvml.ClockGraphics {
		return 1590, nil
	}
	return 5001, nil
}
func (d *mockDeviceWithTemp) GetTemperatureThreshold(ctx context.Context, thresholdType int) (uint32, error) {
	if thresholdType == nvml.TempThresholdShutdown {
		return 90, nil
	}
	return 82, nil
}

type mockDeviceWithMemory struct {
	nvml.Device
	total uint64
	used  uint64
}

func (d *mockDeviceWithMemory) GetMemoryInfo(ctx context.Context) (*nvml.MemoryInfo, error) {
	return &nvml.MemoryInfo{
		Total: d.total,
		Used:  d.used,
		Free:  d.total - d.used,
	}, nil
}

type mockDeviceWithPower struct {
	nvml.Device
	power uint32
}

func (d *mockDeviceWithPower) GetPowerUsage(ctx context.Context) (uint32, error) {
	return d.power, nil
}

func (d *mockDeviceWithPower) GetPowerManagementLimit(
	ctx context.Context,
) (uint32, error) {
	return 70000, nil // 70W TDP for T4
}

// mockHealthyNVML returns a single GPU with healthy values
type mockHealthyNVML struct{}

func (m *mockHealthyNVML) Init(ctx context.Context) error     { return nil }
func (m *mockHealthyNVML) Shutdown(ctx context.Context) error { return nil }
func (m *mockHealthyNVML) GetDeviceCount(ctx context.Context) (int, error) {
	return 1, nil
}
func (m *mockHealthyNVML) GetDeviceByIndex(
	ctx context.Context,
	idx int,
) (nvml.Device, error) {
	return &mockHealthyDevice{}, nil
}
func (m *mockHealthyNVML) GetDriverVersion(ctx context.Context) (string, error) {
	return "575.57.08", nil
}
func (m *mockHealthyNVML) GetCudaDriverVersion(ctx context.Context) (string, error) {
	return "12.9", nil
}

type mockHealthyDevice struct{}

func (d *mockHealthyDevice) GetName(ctx context.Context) (string, error) {
	return "Tesla T4", nil
}
func (d *mockHealthyDevice) GetUUID(ctx context.Context) (string, error) {
	return "GPU-12345678-0000-0000-0000-000000000000", nil
}
func (d *mockHealthyDevice) GetPCIInfo(ctx context.Context) (*nvml.PCIInfo, error) {
	return &nvml.PCIInfo{BusID: "0000:00:1E.0", Domain: 0, Bus: 0, Device: 30}, nil
}
func (d *mockHealthyDevice) GetMemoryInfo(ctx context.Context) (*nvml.MemoryInfo, error) {
	return &nvml.MemoryInfo{
		Total: 16106127360, // 15GB
		Used:  469041152,   // ~450MB (3%)
		Free:  15637086208,
	}, nil
}
func (d *mockHealthyDevice) GetTemperature(ctx context.Context) (uint32, error) {
	return 35, nil // Normal temp
}
func (d *mockHealthyDevice) GetPowerUsage(ctx context.Context) (uint32, error) {
	return 14000, nil // 14W of 70W TDP = 20%
}
func (d *mockHealthyDevice) GetUtilizationRates(
	ctx context.Context,
) (*nvml.Utilization, error) {
	return &nvml.Utilization{GPU: 10, Memory: 5}, nil
}
func (d *mockHealthyDevice) GetPowerManagementLimit(
	ctx context.Context,
) (uint32, error) {
	return 70000, nil // 70W TDP for T4
}
func (d *mockHealthyDevice) GetEccMode(
	ctx context.Context,
) (current, pending bool, err error) {
	return false, false, nil // T4 doesn't have ECC
}
func (d *mockHealthyDevice) GetTotalEccErrors(
	ctx context.Context,
	errorType int,
) (uint64, error) {
	return 0, nil
}
func (d *mockHealthyDevice) GetCurrentClocksThrottleReasons(
	ctx context.Context,
) (uint64, error) {
	return 0, nil // No throttling
}
func (d *mockHealthyDevice) GetClockInfo(
	ctx context.Context,
	clockType int,
) (uint32, error) {
	if clockType == nvml.ClockGraphics {
		return 1590, nil
	}
	return 5001, nil // Memory clock
}
func (d *mockHealthyDevice) GetTemperatureThreshold(
	ctx context.Context,
	thresholdType int,
) (uint32, error) {
	if thresholdType == nvml.TempThresholdShutdown {
		return 90, nil
	}
	return 82, nil // Slowdown
}
func (d *mockHealthyDevice) GetCudaComputeCapability(
	ctx context.Context,
) (string, error) {
	return "7.5", nil // Turing (T4)
}
func (d *mockHealthyDevice) GetNvLinkState(
	ctx context.Context,
	link int,
) (bool, error) {
	return false, nil // T4 doesn't have NVLink
}
func (d *mockHealthyDevice) GetNvLinkRemotePciInfo(
	ctx context.Context,
	link int,
) (*nvml.PCIInfo, error) {
	return nil, nil
}
func (d *mockHealthyDevice) GetNvLinkErrorCounter(
	ctx context.Context,
	link int,
	counterType int,
) (uint64, error) {
	return 0, nil
}

// mockEmptyNVML returns 0 devices
type mockEmptyNVML struct{}

func (m *mockEmptyNVML) Init(ctx context.Context) error     { return nil }
func (m *mockEmptyNVML) Shutdown(ctx context.Context) error { return nil }
func (m *mockEmptyNVML) GetDeviceCount(ctx context.Context) (int, error) {
	return 0, nil
}
func (m *mockEmptyNVML) GetDeviceByIndex(
	ctx context.Context,
	idx int,
) (nvml.Device, error) {
	return nil, fmt.Errorf("no devices")
}
func (m *mockEmptyNVML) GetDriverVersion(ctx context.Context) (string, error) {
	return "", fmt.Errorf("no devices")
}
func (m *mockEmptyNVML) GetCudaDriverVersion(ctx context.Context) (string, error) {
	return "", fmt.Errorf("no devices")
}
