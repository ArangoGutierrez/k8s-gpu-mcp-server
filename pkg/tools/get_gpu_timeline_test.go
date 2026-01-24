// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
)

// setupMockRecorder creates a recorder with mock NVML and test data.
// TODO: The snapshots parameter is reserved for future test data injection
// when the Recorder API supports it.
func setupMockRecorder(t *testing.T, _ []blackbox.GPUSnapshot) (*blackbox.Recorder, nvml.Interface) {
	t.Helper()

	// Create mock NVML client with 2 devices
	mockNVML := nvml.NewMock(2)

	// Create recorder config
	config := blackbox.RecorderConfig{
		Interval:  1 * time.Second,
		Retention: 30 * time.Minute,
	}

	recorder := blackbox.NewRecorder(mockNVML, config)

	// Start the recorder to initialize buffers
	ctx := context.Background()
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("failed to start recorder: %v", err)
	}

	// Note: In a real scenario, we'd inject snapshots into the ring buffer.
	// Since we can't easily do that with the current API, we rely on the
	// recorder collecting initial samples during Start().

	return recorder, mockNVML
}

func TestGetGPUTimelineHandler_Handle_NoRecorder(t *testing.T) {
	mockNVML := nvml.NewMock(2)
	handler := NewGPUTimelineHandler(mockNVML)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result when recorder is nil")
	}

	text := extractTimelineTextContent(t, result)
	if !strings.Contains(text, "flight recorder not available") {
		t.Errorf("expected error about flight recorder, got: %s", text)
	}
}

func TestGetGPUTimelineHandler_Handle_InvalidDuration(t *testing.T) {
	recorder, mockNVML := setupMockRecorder(t, nil)
	defer recorder.Stop()

	handler := NewGPUTimelineHandler(mockNVML,
		WithTimelineRecorder(recorder))

	tests := []struct {
		name     string
		duration string
		wantErr  string
	}{
		{
			name:     "invalid format",
			duration: "abc",
			wantErr:  "invalid duration",
		},
		{
			name:     "negative duration",
			duration: "-10m",
			wantErr:  "must be positive",
		},
		{
			name:     "exceeds maximum",
			duration: "48h",
			wantErr:  "exceeds maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{
				"duration": tt.duration,
			}

			result, err := handler.Handle(context.Background(), request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !result.IsError {
				t.Error("expected error result for invalid duration")
			}

			text := extractTimelineTextContent(t, result)
			if !strings.Contains(text, tt.wantErr) {
				t.Errorf("expected error containing %q, got: %s", tt.wantErr, text)
			}
		})
	}
}

func TestGetGPUTimelineHandler_Handle_InvalidGPUIndex(t *testing.T) {
	recorder, mockNVML := setupMockRecorder(t, nil)
	defer recorder.Stop()

	handler := NewGPUTimelineHandler(mockNVML,
		WithTimelineRecorder(recorder))

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"gpu_index": float64(999), // Out of range
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for invalid GPU index")
	}

	text := extractTimelineTextContent(t, result)
	if !strings.Contains(text, "out of range") {
		t.Errorf("expected error about 'out of range', got: %s", text)
	}
}

func TestGetGPUTimelineHandler_Handle_UnknownUUID(t *testing.T) {
	recorder, mockNVML := setupMockRecorder(t, nil)
	defer recorder.Stop()

	handler := NewGPUTimelineHandler(mockNVML,
		WithTimelineRecorder(recorder))

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"gpu_uuid": "GPU-NONEXISTENT-UUID",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for unknown UUID")
	}

	text := extractTimelineTextContent(t, result)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected error about 'not found', got: %s", text)
	}
}

func TestGetGPUTimelineHandler_Handle_NegativeIndex(t *testing.T) {
	recorder, mockNVML := setupMockRecorder(t, nil)
	defer recorder.Stop()

	handler := NewGPUTimelineHandler(mockNVML,
		WithTimelineRecorder(recorder))

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"gpu_index": float64(-1),
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for negative GPU index")
	}
}

func TestGetGPUTimelineHandler_Handle_AllGPUs(t *testing.T) {
	recorder, mockNVML := setupMockRecorder(t, nil)
	defer recorder.Stop()

	handler := NewGPUTimelineHandler(mockNVML,
		WithTimelineRecorder(recorder))

	// Wait a moment for initial samples
	time.Sleep(100 * time.Millisecond)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"duration": "1m",
	}

	result, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		text := extractTimelineTextContent(t, result)
		t.Errorf("unexpected error result: %s", text)
	}

	// Parse response
	text := extractTimelineTextContent(t, result)
	var response MultiGPUTimelineResponse
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.GPUCount != 2 {
		t.Errorf("expected 2 GPUs, got %d", response.GPUCount)
	}

	if len(response.Timelines) != 2 {
		t.Errorf("expected 2 timelines, got %d", len(response.Timelines))
	}
}

func TestGetGPUTimelineHandler_parseArgs_Defaults(t *testing.T) {
	mockNVML := nvml.NewMock(2)
	handler := NewGPUTimelineHandler(mockNVML)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{}

	args, err := handler.parseArgs(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if args.duration != DefaultTimelineDuration {
		t.Errorf("expected default duration %v, got %v", DefaultTimelineDuration, args.duration)
	}

	if !args.includeStats {
		t.Error("expected include_stats to default to true")
	}

	if args.gpuUUID != "" {
		t.Error("expected gpu_uuid to be empty by default")
	}

	if args.gpuIndex != nil {
		t.Error("expected gpu_index to be nil by default")
	}
}

func TestGetGPUTimelineHandler_parseArgs_CustomValues(t *testing.T) {
	mockNVML := nvml.NewMock(2)
	handler := NewGPUTimelineHandler(mockNVML)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"gpu_uuid":      "GPU-TEST-UUID",
		"duration":      "10m",
		"include_stats": false,
	}

	args, err := handler.parseArgs(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if args.gpuUUID != "GPU-TEST-UUID" {
		t.Errorf("expected gpu_uuid 'GPU-TEST-UUID', got %s", args.gpuUUID)
	}

	if args.duration != 10*time.Minute {
		t.Errorf("expected duration 10m, got %v", args.duration)
	}

	if args.includeStats {
		t.Error("expected include_stats to be false")
	}
}

func TestGetGPUTimelineHandler_parseArgs_GPUIndex(t *testing.T) {
	mockNVML := nvml.NewMock(2)
	handler := NewGPUTimelineHandler(mockNVML)

	tests := []struct {
		name      string
		indexVal  any
		wantIndex *int
		wantErr   bool
	}{
		{
			name:      "float64 index (from JSON)",
			indexVal:  float64(1),
			wantIndex: intPtr(1),
			wantErr:   false,
		},
		{
			name:      "int index",
			indexVal:  2,
			wantIndex: intPtr(2),
			wantErr:   false,
		},
		{
			name:      "zero index",
			indexVal:  float64(0),
			wantIndex: intPtr(0),
			wantErr:   false,
		},
		{
			name:     "negative index",
			indexVal: float64(-1),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{
				"gpu_index": tt.indexVal,
			}

			args, err := handler.parseArgs(request)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if args.gpuIndex == nil {
				if tt.wantIndex != nil {
					t.Errorf("expected index %d, got nil", *tt.wantIndex)
				}
			} else {
				if tt.wantIndex == nil {
					t.Errorf("expected nil index, got %d", *args.gpuIndex)
				} else if *args.gpuIndex != *tt.wantIndex {
					t.Errorf("expected index %d, got %d", *tt.wantIndex, *args.gpuIndex)
				}
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"10m", 10 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"30s", 30 * time.Second, false},
		{"2h30m", 2*time.Hour + 30*time.Minute, false},
		{"1.5h", 90 * time.Minute, false},
		{"invalid", 0, true},
		{"", 0, true},
		{"10", 0, true}, // missing unit
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestComputeStatistics_Empty(t *testing.T) {
	stats := computeStatistics(nil)
	if stats != nil {
		t.Error("expected nil stats for empty snapshots")
	}

	stats = computeStatistics([]blackbox.GPUSnapshot{})
	if stats != nil {
		t.Error("expected nil stats for empty slice")
	}
}

func TestComputeStatistics_Single(t *testing.T) {
	snapshots := []blackbox.GPUSnapshot{{
		Temperature: 65,
		PowerMW:     250000,
		GPUUtil:     90,
		MemUtil:     80,
	}}

	stats := computeStatistics(snapshots)
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}

	// With a single sample, min=max=avg
	if stats.Temperature.Min != 65 || stats.Temperature.Max != 65 {
		t.Errorf("expected temp min=max=65, got min=%d max=%d",
			stats.Temperature.Min, stats.Temperature.Max)
	}

	if stats.Temperature.Avg != 65.0 {
		t.Errorf("expected temp avg=65.0, got %f", stats.Temperature.Avg)
	}
}

func TestComputeStatistics_Multiple(t *testing.T) {
	snapshots := []blackbox.GPUSnapshot{
		{Temperature: 60, PowerMW: 200000, GPUUtil: 50, MemUtil: 40},
		{Temperature: 70, PowerMW: 300000, GPUUtil: 90, MemUtil: 80},
		{Temperature: 65, PowerMW: 250000, GPUUtil: 70, MemUtil: 60},
	}

	stats := computeStatistics(snapshots)
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}

	// Temperature: min=60, max=70, avg=65
	if stats.Temperature.Min != 60 {
		t.Errorf("expected temp min=60, got %d", stats.Temperature.Min)
	}
	if stats.Temperature.Max != 70 {
		t.Errorf("expected temp max=70, got %d", stats.Temperature.Max)
	}
	if stats.Temperature.Avg != 65.0 {
		t.Errorf("expected temp avg=65.0, got %f", stats.Temperature.Avg)
	}

	// Power: min=200000, max=300000, avg=250000
	if stats.PowerMW.Min != 200000 {
		t.Errorf("expected power min=200000, got %d", stats.PowerMW.Min)
	}
	if stats.PowerMW.Max != 300000 {
		t.Errorf("expected power max=300000, got %d", stats.PowerMW.Max)
	}
	if stats.PowerMW.Avg != 250000.0 {
		t.Errorf("expected power avg=250000.0, got %f", stats.PowerMW.Avg)
	}

	// GPU Util: min=50, max=90, avg=70
	if stats.GPUUtil.Min != 50 {
		t.Errorf("expected gpu util min=50, got %d", stats.GPUUtil.Min)
	}
	if stats.GPUUtil.Max != 90 {
		t.Errorf("expected gpu util max=90, got %d", stats.GPUUtil.Max)
	}
	if stats.GPUUtil.Avg != 70.0 {
		t.Errorf("expected gpu util avg=70.0, got %f", stats.GPUUtil.Avg)
	}
}

func TestDetectThrottlingTransitions(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		snapshots []blackbox.GPUSnapshot
		wantCount int
		wantTypes []string
	}{
		{
			name:      "empty snapshots",
			snapshots: nil,
			wantCount: 0,
		},
		{
			name: "single snapshot",
			snapshots: []blackbox.GPUSnapshot{
				{Timestamp: now, Throttling: 1},
			},
			wantCount: 0,
		},
		{
			name: "no transitions",
			snapshots: []blackbox.GPUSnapshot{
				{Timestamp: now, Throttling: 0},
				{Timestamp: now.Add(time.Second), Throttling: 0},
				{Timestamp: now.Add(2 * time.Second), Throttling: 0},
			},
			wantCount: 0,
		},
		{
			name: "throttle start",
			snapshots: []blackbox.GPUSnapshot{
				{Timestamp: now, Throttling: 0},
				{Timestamp: now.Add(time.Second), Throttling: 1},
			},
			wantCount: 1,
			wantTypes: []string{"throttle_start"},
		},
		{
			name: "throttle end",
			snapshots: []blackbox.GPUSnapshot{
				{Timestamp: now, Throttling: 1},
				{Timestamp: now.Add(time.Second), Throttling: 0},
			},
			wantCount: 1,
			wantTypes: []string{"throttle_end"},
		},
		{
			name: "multiple transitions",
			snapshots: []blackbox.GPUSnapshot{
				{Timestamp: now, Throttling: 0},
				{Timestamp: now.Add(time.Second), Throttling: 1},     // start
				{Timestamp: now.Add(2 * time.Second), Throttling: 1}, // no change
				{Timestamp: now.Add(3 * time.Second), Throttling: 0}, // end
				{Timestamp: now.Add(4 * time.Second), Throttling: 1}, // start again
			},
			wantCount: 3,
			wantTypes: []string{"throttle_start", "throttle_end", "throttle_start"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := detectThrottlingTransitions(tt.snapshots)

			if len(events) != tt.wantCount {
				t.Errorf("expected %d events, got %d", tt.wantCount, len(events))
			}

			for i, wantType := range tt.wantTypes {
				if i >= len(events) {
					break
				}
				if events[i].Type != wantType {
					t.Errorf("event %d: expected type %q, got %q", i, wantType, events[i].Type)
				}
			}
		})
	}
}

func TestGetGPUTimelineTool(t *testing.T) {
	tool := GetGPUTimelineTool()

	if tool.Name != "get_gpu_timeline" {
		t.Errorf("expected tool name 'get_gpu_timeline', got %s", tool.Name)
	}

	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	// Verify description mentions key features
	if !strings.Contains(tool.Description, "historical") {
		t.Error("expected description to mention 'historical'")
	}
	if !strings.Contains(tool.Description, "flight recorder") {
		t.Error("expected description to mention 'flight recorder'")
	}
}

func TestGPUTimelineHandler_WithOptions(t *testing.T) {
	mockNVML := nvml.NewMock(2)

	t.Run("WithTimelineRecorder sets recorder", func(t *testing.T) {
		recorder, _ := setupMockRecorder(t, nil)
		defer recorder.Stop()

		handler := NewGPUTimelineHandler(mockNVML,
			WithTimelineRecorder(recorder))

		if handler.recorder != recorder {
			t.Error("expected recorder to be set")
		}
	})

	t.Run("WithTimelineXIDWatcher sets watcher", func(t *testing.T) {
		handler := NewGPUTimelineHandler(mockNVML,
			WithTimelineXIDWatcher(nil))

		if handler.xidWatcher != nil {
			t.Error("expected xidWatcher to be nil")
		}
	})

	t.Run("default values when no options", func(t *testing.T) {
		handler := NewGPUTimelineHandler(mockNVML)

		if handler.recorder != nil {
			t.Error("expected recorder to be nil by default")
		}
		if handler.xidWatcher != nil {
			t.Error("expected xidWatcher to be nil by default")
		}
		if handler.nvmlClient != mockNVML {
			t.Error("expected nvmlClient to be set")
		}
	})
}

func TestGPUTimelineHandler_BuildResponse(t *testing.T) {
	// NewMock creates devices automatically with mock UUIDs
	mockNVML := nvml.NewMock(1)

	handler := NewGPUTimelineHandler(mockNVML)

	now := time.Now()
	snapshots := []blackbox.GPUSnapshot{
		{
			Timestamp:   now,
			Index:       0,
			UUID:        "GPU-TEST-UUID",
			Temperature: 60,
			PowerMW:     200000,
			GPUUtil:     50,
			MemUtil:     40,
			MemUsed:     10 * 1024 * 1024 * 1024, // 10GB
			MemTotal:    40 * 1024 * 1024 * 1024, // 40GB
			Throttling:  0,
		},
		{
			Timestamp:   now.Add(time.Second),
			Index:       0,
			UUID:        "GPU-TEST-UUID",
			Temperature: 65,
			PowerMW:     250000,
			GPUUtil:     70,
			MemUtil:     60,
			MemUsed:     15 * 1024 * 1024 * 1024,
			MemTotal:    40 * 1024 * 1024 * 1024,
			Throttling:  0,
		},
	}

	args := &timelineArgs{
		duration:     10 * time.Minute,
		includeStats: true,
	}

	resp := handler.buildResponse(context.Background(), "GPU-TEST-UUID", snapshots, args)

	if resp.GPUUUID != "GPU-TEST-UUID" {
		t.Errorf("expected UUID 'GPU-TEST-UUID', got %s", resp.GPUUUID)
	}

	if resp.GPUIndex != 0 {
		t.Errorf("expected index 0, got %d", resp.GPUIndex)
	}

	if resp.SampleCount != 2 {
		t.Errorf("expected 2 samples, got %d", resp.SampleCount)
	}

	if len(resp.DataPoints) != 2 {
		t.Errorf("expected 2 data points, got %d", len(resp.DataPoints))
	}

	if resp.Statistics == nil {
		t.Error("expected non-nil statistics when includeStats=true")
	}

	// Verify data point structure
	dp := resp.DataPoints[0]
	if dp.TemperatureCelsius != 60 {
		t.Errorf("expected temp 60, got %d", dp.TemperatureCelsius)
	}
	if dp.PowerMW != 200000 {
		t.Errorf("expected power 200000, got %d", dp.PowerMW)
	}
}

func TestGPUTimelineHandler_BuildResponse_NoStats(t *testing.T) {
	mockNVML := nvml.NewMock(2)
	handler := NewGPUTimelineHandler(mockNVML)

	now := time.Now()
	snapshots := []blackbox.GPUSnapshot{
		{Timestamp: now, Index: 0, Temperature: 60},
	}

	args := &timelineArgs{
		duration:     10 * time.Minute,
		includeStats: false, // Explicitly disable stats
	}

	resp := handler.buildResponse(context.Background(), "GPU-TEST-UUID", snapshots, args)

	if resp.Statistics != nil {
		t.Error("expected nil statistics when includeStats=false")
	}
}

func TestGPUTimelineHandler_ResolveGPUUUID(t *testing.T) {
	recorder, mockNVML := setupMockRecorder(t, nil)
	defer recorder.Stop()

	handler := NewGPUTimelineHandler(mockNVML,
		WithTimelineRecorder(recorder))

	ctx := context.Background()

	t.Run("resolve by index", func(t *testing.T) {
		index := 0
		uuid, err := handler.resolveGPUUUID(ctx, "", &index)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if uuid == "" {
			t.Error("expected non-empty UUID")
		}
	})

	t.Run("all GPUs when nothing specified", func(t *testing.T) {
		uuid, err := handler.resolveGPUUUID(ctx, "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if uuid != "" {
			t.Errorf("expected empty UUID for all GPUs, got %s", uuid)
		}
	})

	t.Run("unknown UUID returns error", func(t *testing.T) {
		_, err := handler.resolveGPUUUID(ctx, "UNKNOWN-UUID", nil)
		if err == nil {
			t.Error("expected error for unknown UUID")
		}
	})

	t.Run("out of range index returns error", func(t *testing.T) {
		index := 999
		_, err := handler.resolveGPUUUID(ctx, "", &index)
		if err == nil {
			t.Error("expected error for out of range index")
		}
	})
}

func TestGPUTimelineHandler_ResolveGPUUUID_NoNVML(t *testing.T) {
	// Create handler without NVML client
	handler := &GPUTimelineHandler{
		nvmlClient: nil,
	}

	ctx := context.Background()
	index := 0

	_, err := handler.resolveGPUUUID(ctx, "", &index)
	if err == nil {
		t.Error("expected error when NVML client is nil")
	}

	if !strings.Contains(err.Error(), "NVML client") {
		t.Errorf("expected error about NVML client, got: %v", err)
	}
}

// Helper functions

func extractTimelineTextContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content in result")
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return content.Text
}

func intPtr(i int) *int {
	return &i
}
