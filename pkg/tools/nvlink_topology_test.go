// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestNVLinkTopology_NoGPUs(t *testing.T) {
	// Use custom mock that returns 0 devices (NewMock(0) defaults to 2)
	mock := &mockZeroGPUs{}
	handler := NewNVLinkTopologyHandler(mock)

	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response NVLinkTopologyResponse
	if err := unmarshalToolResult(t, result, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.GPUCount != 0 {
		t.Errorf("expected gpu_count=0, got %d", response.GPUCount)
	}
	if len(response.Links) != 0 {
		t.Errorf("expected empty links, got %d", len(response.Links))
	}
	if response.HealthStatus != "unknown" {
		t.Errorf("expected health_status=unknown, got %s", response.HealthStatus)
	}
	if response.Summary != "No GPU devices detected" {
		t.Errorf("unexpected summary: %s", response.Summary)
	}
}

// mockZeroGPUs returns 0 devices for testing empty GPU case
type mockZeroGPUs struct{}

func (m *mockZeroGPUs) Init(ctx context.Context) error     { return nil }
func (m *mockZeroGPUs) Shutdown(ctx context.Context) error { return nil }
func (m *mockZeroGPUs) GetDeviceCount(ctx context.Context) (int, error) {
	return 0, nil
}
func (m *mockZeroGPUs) GetDeviceByIndex(ctx context.Context, idx int) (nvml.Device, error) {
	return nil, nil
}
func (m *mockZeroGPUs) GetDriverVersion(ctx context.Context) (string, error) {
	return "", nil
}
func (m *mockZeroGPUs) GetCudaDriverVersion(ctx context.Context) (string, error) {
	return "", nil
}
func (m *mockZeroGPUs) GetCapabilities(ctx context.Context) (*nvml.Capabilities, error) {
	return nil, nil
}

func TestNVLinkTopology_SingleGPU(t *testing.T) {
	mock := nvml.NewMock(1)
	handler := NewNVLinkTopologyHandler(mock)

	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response NVLinkTopologyResponse
	if err := unmarshalToolResult(t, result, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.GPUCount != 1 {
		t.Errorf("expected gpu_count=1, got %d", response.GPUCount)
	}
	if len(response.Links) != 0 {
		t.Errorf("expected empty links for single GPU, got %d", len(response.Links))
	}
	if response.HealthStatus != "healthy" {
		t.Errorf("expected health_status=healthy, got %s", response.HealthStatus)
	}
	if response.Summary != "Single GPU system, no NVLink connections" {
		t.Errorf("unexpected summary: %s", response.Summary)
	}
}

func TestNVLinkTopology_TwoGPUsNoNVLink(t *testing.T) {
	mock := nvml.NewMock(2)
	handler := NewNVLinkTopologyHandler(mock)

	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response NVLinkTopologyResponse
	if err := unmarshalToolResult(t, result, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.GPUCount != 2 {
		t.Errorf("expected gpu_count=2, got %d", response.GPUCount)
	}
	if len(response.Links) != 0 {
		t.Errorf("expected empty links (no NVLink support), got %d", len(response.Links))
	}
	if response.HealthStatus != "healthy" {
		t.Errorf("expected health_status=healthy, got %s", response.HealthStatus)
	}
}

func TestNVLinkTopology_TwoGPUsWithNVLink(t *testing.T) {
	mock := nvml.NewMock(2)

	// Configure NVLink topology: GPU 0 link 0 -> GPU 1
	dev0 := mock.GetMockDevice(0)
	dev0.SetNVLinkTopology(nvml.NVLinkTopology{0: 1})

	// GPU 1 link 0 -> GPU 0 (bidirectional)
	dev1 := mock.GetMockDevice(1)
	dev1.SetNVLinkTopology(nvml.NVLinkTopology{0: 0})

	handler := NewNVLinkTopologyHandler(mock)
	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response NVLinkTopologyResponse
	if err := unmarshalToolResult(t, result, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.GPUCount != 2 {
		t.Errorf("expected gpu_count=2, got %d", response.GPUCount)
	}
	if len(response.Links) != 1 {
		t.Errorf("expected 1 link (deduplicated), got %d", len(response.Links))
	}
	if response.HealthStatus != "healthy" {
		t.Errorf("expected health_status=healthy, got %s", response.HealthStatus)
	}

	// Verify link details
	if len(response.Links) > 0 {
		link := response.Links[0]
		if link.GPU1 != 0 || link.GPU2 != 1 {
			t.Errorf("expected GPU1=0, GPU2=1, got %d, %d", link.GPU1, link.GPU2)
		}
		if link.LinkType != "NVLink" {
			t.Errorf("expected LinkType=NVLink, got %s", link.LinkType)
		}
		if !link.Active {
			t.Error("expected link to be active")
		}
		if link.ErrorCount != 0 {
			t.Errorf("expected ErrorCount=0, got %d", link.ErrorCount)
		}
	}
}

func TestNVLinkTopology_DegradedLink(t *testing.T) {
	mock := nvml.NewMock(2)

	// Configure NVLink with low errors (< 1000 total after summing 5 counters)
	// Mock returns same error value for all 5 counter types, so 50 * 5 = 250
	dev0 := mock.GetMockDevice(0)
	dev0.SetNVLinkTopology(nvml.NVLinkTopology{0: 1})
	dev0.SetNVLinkErrors(map[int]uint64{0: 50})

	dev1 := mock.GetMockDevice(1)
	dev1.SetNVLinkTopology(nvml.NVLinkTopology{0: 0})
	dev1.SetNVLinkErrors(map[int]uint64{0: 50})

	handler := NewNVLinkTopologyHandler(mock)
	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response NVLinkTopologyResponse
	if err := unmarshalToolResult(t, result, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.HealthStatus != "warning" {
		t.Errorf("expected health_status=warning, got %s", response.HealthStatus)
	}

	// Verify error count in link (50 * 5 counters = 250)
	if len(response.Links) > 0 {
		if response.Links[0].ErrorCount < 250 {
			t.Errorf("expected ErrorCount >= 250, got %d",
				response.Links[0].ErrorCount)
		}
	}
}

func TestNVLinkTopology_HighErrorCount(t *testing.T) {
	mock := nvml.NewMock(2)

	// Configure NVLink with high errors (>1000 triggers degraded)
	dev0 := mock.GetMockDevice(0)
	dev0.SetNVLinkTopology(nvml.NVLinkTopology{0: 1})
	dev0.SetNVLinkErrors(map[int]uint64{0: 2000})

	dev1 := mock.GetMockDevice(1)
	dev1.SetNVLinkTopology(nvml.NVLinkTopology{0: 0})

	handler := NewNVLinkTopologyHandler(mock)
	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response NVLinkTopologyResponse
	if err := unmarshalToolResult(t, result, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.HealthStatus != "degraded" {
		t.Errorf("expected health_status=degraded, got %s", response.HealthStatus)
	}
}

func TestNVLinkTopology_MultipleLinks(t *testing.T) {
	mock := nvml.NewMock(4)

	// Configure full mesh: 0-1, 0-2, 1-2, 1-3, 2-3
	mock.GetMockDevice(0).SetNVLinkTopology(nvml.NVLinkTopology{0: 1, 1: 2})
	mock.GetMockDevice(1).SetNVLinkTopology(nvml.NVLinkTopology{0: 0, 1: 2, 2: 3})
	mock.GetMockDevice(2).SetNVLinkTopology(nvml.NVLinkTopology{0: 0, 1: 1, 2: 3})
	mock.GetMockDevice(3).SetNVLinkTopology(nvml.NVLinkTopology{0: 1, 1: 2})

	handler := NewNVLinkTopologyHandler(mock)
	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response NVLinkTopologyResponse
	if err := unmarshalToolResult(t, result, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.GPUCount != 4 {
		t.Errorf("expected gpu_count=4, got %d", response.GPUCount)
	}

	// Should have 5 unique links after deduplication
	expectedLinks := 5
	if len(response.Links) != expectedLinks {
		t.Errorf("expected %d unique links, got %d", expectedLinks, len(response.Links))
	}

	// Verify links are sorted
	for i := 1; i < len(response.Links); i++ {
		prev := response.Links[i-1]
		curr := response.Links[i]
		if prev.GPU1 > curr.GPU1 ||
			(prev.GPU1 == curr.GPU1 && prev.GPU2 > curr.GPU2) {
			t.Error("links are not sorted correctly")
		}
	}
}

func TestNVLinkTopology_ContextCancellation(t *testing.T) {
	mock := nvml.NewMock(2)
	handler := NewNVLinkTopologyHandler(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := handler.Handle(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return error result, not panic
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// The result should be an error result
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
}

func TestNVLinkTopology_SummaryGeneration(t *testing.T) {
	tests := []struct {
		name          string
		gpuCount      int
		setupTopology func(*nvml.Mock)
		wantContains  string
	}{
		{
			name:          "single GPU",
			gpuCount:      1,
			setupTopology: func(m *nvml.Mock) {},
			wantContains:  "Single GPU",
		},
		{
			name:          "multiple GPUs no NVLink",
			gpuCount:      4,
			setupTopology: func(m *nvml.Mock) {},
			wantContains:  "PCIe only",
		},
		{
			name:     "GPUs with NVLink",
			gpuCount: 2,
			setupTopology: func(m *nvml.Mock) {
				m.GetMockDevice(0).SetNVLinkTopology(nvml.NVLinkTopology{0: 1})
				m.GetMockDevice(1).SetNVLinkTopology(nvml.NVLinkTopology{0: 0})
			},
			wantContains: "NVLink connection",
		},
		{
			name:     "degraded link in summary",
			gpuCount: 2,
			setupTopology: func(m *nvml.Mock) {
				m.GetMockDevice(0).SetNVLinkTopology(nvml.NVLinkTopology{0: 1})
				m.GetMockDevice(0).SetNVLinkErrors(map[int]uint64{0: 100})
				m.GetMockDevice(1).SetNVLinkTopology(nvml.NVLinkTopology{0: 0})
			},
			wantContains: "Degraded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := nvml.NewMock(tt.gpuCount)
			tt.setupTopology(mock)

			handler := NewNVLinkTopologyHandler(mock)
			result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var response NVLinkTopologyResponse
			if err := unmarshalToolResult(t, result, &response); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if !strings.Contains(response.Summary, tt.wantContains) {
				t.Errorf("summary %q does not contain %q",
					response.Summary, tt.wantContains)
			}
		})
	}
}

func TestGetNVLinkTopologyTool(t *testing.T) {
	tool := GetNVLinkTopologyTool()

	if tool.Name != "get_nvlink_topology" {
		t.Errorf("expected tool name get_nvlink_topology, got %s", tool.Name)
	}

	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	if !strings.Contains(tool.Description, "NVLink") {
		t.Error("description should mention NVLink")
	}
}

// Helper to unmarshal tool result content
func unmarshalToolResult(t *testing.T, result *mcp.CallToolResult, v any) error {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("empty result content")
	}

	// Get text content
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	return json.Unmarshal([]byte(textContent.Text), v)
}
