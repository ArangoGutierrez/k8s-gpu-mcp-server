// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIVersionConstant(t *testing.T) {
	assert.Equal(t, "v1", APIVersion, "APIVersion constant must be v1")
}

// TestAPIVersionInGPUInventoryResponse verifies that the gpu_inventory handler
// includes api_version in its JSON response.
func TestAPIVersionInGPUInventoryResponse(t *testing.T) {
	mockClient := nvml.NewMock(1)
	handler := NewGPUInventoryHandler(mockClient)

	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, result)

	text := extractTextContent(t, result)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	assert.Equal(t, "v1", resp["api_version"], "gpu_inventory response must contain api_version")
}

// TestAPIVersionInGPUHealthResponse verifies that the gpu_health handler
// includes api_version in its JSON response.
func TestAPIVersionInGPUHealthResponse(t *testing.T) {
	mockClient := nvml.NewMock(1)
	handler := NewGPUHealthHandler(mockClient)

	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, result)

	text := extractTextContent(t, result)
	var resp GPUHealthResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	assert.Equal(t, "v1", resp.APIVersion, "gpu_health response must contain api_version")
}

// TestAPIVersionInNVLinkTopologyResponse verifies that the nvlink_topology
// handler includes api_version in its JSON response.
func TestAPIVersionInNVLinkTopologyResponse(t *testing.T) {
	mockClient := nvml.NewMock(2)
	handler := NewNVLinkTopologyHandler(mockClient)

	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, result)

	text := extractTextContent(t, result)
	var resp NVLinkTopologyResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	assert.Equal(t, "v1", resp.APIVersion, "nvlink_topology response must contain api_version")
}

// TestAPIVersionInAnalyzeXIDResponse verifies that the analyze_xid handler
// includes api_version in its JSON response.
func TestAPIVersionInAnalyzeXIDResponse(t *testing.T) {
	mockClient := nvml.NewMock(1)
	handler := NewAnalyzeXIDHandler(mockClient)
	// Inject a mock parser that returns no events
	handler.parser = &mockXIDParser{events: nil, err: nil}

	result, err := handler.Handle(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, result)

	text := extractTextContent(t, result)
	var resp AnalyzeXIDResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	assert.Equal(t, "v1", resp.APIVersion, "analyze_xid response must contain api_version")
}

// extractTextContent is defined in explain_failure_test.go (same package).
