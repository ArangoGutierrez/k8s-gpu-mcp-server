// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNegative_MissingRequiredArguments verifies that tools requiring
// specific arguments return structured JSON-RPC errors when arguments
// are missing, rather than crashing or returning ambiguous responses.
func TestNegative_MissingRequiredArguments(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		desc     string
	}{
		{
			name:     "describe_gpu_node without node_name",
			toolName: "describe_gpu_node",
			args:     nil,
			desc:     "describe_gpu_node requires node_name",
		},
		{
			name:     "describe_gpu_node with empty node_name",
			toolName: "describe_gpu_node",
			args:     map[string]interface{}{"node_name": ""},
			desc:     "empty node_name should be handled gracefully",
		},
		{
			name:     "tools/call with empty tool name",
			toolName: "",
			args:     nil,
			desc:     "empty tool name should return error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := callTool(t, tt.toolName, tt.args)

			assert.Equal(t, "2.0", resp.JSONRPC,
				"Response must be valid JSON-RPC 2.0")

			// The server should either return a JSON-RPC error or
			// a tool result with isError=true. Both are acceptable
			// as long as the response is well-formed.
			if resp.Error != nil {
				t.Logf("[%s] JSON-RPC error: code=%d, msg=%s",
					tt.desc, resp.Error.Code, resp.Error.Message)
				assert.NotZero(t, resp.Error.Code,
					"Error code should be non-zero")
				assert.NotEmpty(t, resp.Error.Message,
					"Error message should not be empty")
			} else {
				require.NotNil(t, resp.Result,
					"If no error, result must be present")
				// Verify the result is parseable
				var result map[string]interface{}
				err := json.Unmarshal(resp.Result, &result)
				require.NoError(t, err,
					"Result should be valid JSON")
				t.Logf("[%s] Got result (tool handled gracefully): %s",
					tt.desc, string(resp.Result))
			}
		})
	}
}

// TestNegative_InvalidNodeNames verifies that tools receiving nonexistent
// node names return graceful errors instead of panicking or hanging.
func TestNegative_InvalidNodeNames(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	invalidNames := []struct {
		name     string
		nodeName string
	}{
		{"nonexistent node", "node-that-does-not-exist-12345"},
		{"special characters", "node/with/slashes"},
		{"very long name", "a-very-long-node-name-that-exceeds-any-reasonable-length-for-a-kubernetes-node-name-and-should-be-rejected-gracefully-by-the-server-without-crashing"},
		{"unicode characters", "node-\u00e9\u00e8\u00ea"},
	}

	for _, tt := range invalidNames {
		t.Run(tt.name, func(t *testing.T) {
			resp := callTool(t, "describe_gpu_node", map[string]interface{}{
				"node_name": tt.nodeName,
			})

			assert.Equal(t, "2.0", resp.JSONRPC,
				"Response must be valid JSON-RPC 2.0")

			// The server should handle invalid node names without crashing.
			// It may return an error or a result indicating the node was not found.
			if resp.Error != nil {
				t.Logf("describe_gpu_node(%q) error: code=%d, msg=%s",
					tt.nodeName, resp.Error.Code, resp.Error.Message)
				assert.NotEqual(t, -32603, resp.Error.Code,
					"Should not be an internal server error (-32603)")
			} else {
				require.NotNil(t, resp.Result,
					"If no error, result must be present")
				var result map[string]interface{}
				err := json.Unmarshal(resp.Result, &result)
				require.NoError(t, err)
				t.Logf("describe_gpu_node(%q) result: %s",
					tt.nodeName, string(resp.Result))
			}
		})
	}
}

// TestNegative_InvalidNodeNameForPodAllocation verifies
// get_pod_gpu_allocation handles invalid node names gracefully.
func TestNegative_InvalidNodeNameForPodAllocation(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	resp := callTool(t, "get_pod_gpu_allocation", map[string]interface{}{
		"node_name": "nonexistent-node-xyz",
	})

	assert.Equal(t, "2.0", resp.JSONRPC)

	// Should handle gracefully - either error or empty result
	if resp.Error != nil {
		t.Logf("get_pod_gpu_allocation(nonexistent) error: code=%d, msg=%s",
			resp.Error.Code, resp.Error.Message)
		assert.NotEqual(t, -32603, resp.Error.Code,
			"Should not be internal server error")
	} else {
		require.NotNil(t, resp.Result)
		t.Logf("get_pod_gpu_allocation(nonexistent) result: %s",
			string(resp.Result))
	}
}

// TestNegative_GPUUnavailableScenario verifies tool behavior in a
// KIND cluster where no real GPUs are present. In mock mode, tools
// should still return valid responses. This test validates the
// degraded-but-functional contract.
func TestNegative_GPUUnavailableScenario(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	// In a KIND cluster there are no real GPUs. The server runs in
	// mock mode but the protocol contract must still hold: every
	// tool must return either a valid MCP content response or a
	// well-formed JSON-RPC error.
	tools := []string{
		"get_gpu_inventory",
		"get_gpu_health",
		"analyze_xid_errors",
		"get_pod_gpu_allocation",
	}

	for _, toolName := range tools {
		t.Run(toolName, func(t *testing.T) {
			resp := callTool(t, toolName, nil)

			assert.Equal(t, "2.0", resp.JSONRPC,
				"%s: must return JSON-RPC 2.0", toolName)

			if resp.Error != nil {
				// If error, it should be well-structured
				t.Logf("%s returned error (acceptable in no-GPU env): code=%d, msg=%s",
					toolName, resp.Error.Code, resp.Error.Message)
				assert.NotZero(t, resp.Error.Code)
				assert.NotEmpty(t, resp.Error.Message)
				return
			}

			require.NotNil(t, resp.Result,
				"%s: must return result when no error", toolName)

			// Verify the MCP content structure is valid
			var result map[string]interface{}
			err := json.Unmarshal(resp.Result, &result)
			require.NoError(t, err, "%s: result must be valid JSON", toolName)

			content, ok := result["content"].([]interface{})
			require.True(t, ok,
				"%s: result must have content array", toolName)
			require.NotEmpty(t, content,
				"%s: content must not be empty", toolName)

			// Each content item must have 'type' field
			for i, item := range content {
				itemMap, ok := item.(map[string]interface{})
				require.True(t, ok,
					"%s: content[%d] must be object", toolName, i)
				assert.Contains(t, itemMap, "type",
					"%s: content[%d] must have type field", toolName, i)
			}
		})
	}
}

// TestNegative_GatewayAggregationValidation validates that the gateway
// correctly aggregates responses from multiple nodes, including the
// aggregation metadata fields.
func TestNegative_GatewayAggregationValidation(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	resp := callTool(t, "get_gpu_inventory", nil)

	assert.Nil(t, resp.Error, "get_gpu_inventory should succeed")
	require.NotNil(t, resp.Result)

	var result map[string]interface{}
	err := json.Unmarshal(resp.Result, &result)
	require.NoError(t, err)

	content, ok := result["content"].([]interface{})
	require.True(t, ok, "Must have content array")
	require.NotEmpty(t, content)

	// The first content item should contain the aggregated text
	firstItem, ok := content[0].(map[string]interface{})
	require.True(t, ok)
	text, ok := firstItem["text"].(string)
	require.True(t, ok, "Content must have text field")

	// Parse the aggregated JSON from the text content
	var aggregated map[string]interface{}
	err = json.Unmarshal([]byte(text), &aggregated)
	if err != nil {
		// If text is not JSON, it may be a formatted string - still valid
		t.Logf("Aggregated response is plain text: %s", text)
		return
	}

	// If JSON, verify gateway aggregation structure
	t.Logf("Aggregated response: %s", text)

	if status, ok := aggregated["status"].(string); ok {
		assert.Contains(t, []string{"success", "partial", "error"}, status,
			"Status must be one of: success, partial, error")
	}

	if nodes, ok := aggregated["nodes"].([]interface{}); ok {
		t.Logf("Gateway aggregated %d node(s)", len(nodes))
		assert.NotEmpty(t, nodes, "Should have at least one node")

		for i, node := range nodes {
			nodeMap, ok := node.(map[string]interface{})
			require.True(t, ok, "Node %d should be an object", i)

			// Each node should have a name or identifier
			hasName := false
			for _, key := range []string{"name", "node_name"} {
				if _, ok := nodeMap[key]; ok {
					hasName = true
					break
				}
			}
			assert.True(t, hasName,
				"Node %d should have name or node_name field", i)
		}
	}

	// Verify cluster_summary if present (inventory-specific)
	if summary, ok := aggregated["cluster_summary"].(map[string]interface{}); ok {
		t.Logf("Cluster summary: %+v", summary)
		assert.Contains(t, summary, "total_nodes",
			"Cluster summary should have total_nodes")
		assert.Contains(t, summary, "total_gpus",
			"Cluster summary should have total_gpus")
	}
}

// TestNegative_GatewayMultiToolAggregation verifies that different tools
// produce consistent aggregation formats from the gateway.
func TestNegative_GatewayMultiToolAggregation(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	tools := []string{
		"get_gpu_health",
		"analyze_xid_errors",
	}

	for _, toolName := range tools {
		t.Run(toolName, func(t *testing.T) {
			resp := callTool(t, toolName, nil)
			if resp.Error != nil {
				t.Skipf("%s returned error, skipping aggregation check", toolName)
			}

			require.NotNil(t, resp.Result)

			var result map[string]interface{}
			err := json.Unmarshal(resp.Result, &result)
			require.NoError(t, err)

			content, ok := result["content"].([]interface{})
			require.True(t, ok)
			require.NotEmpty(t, content)

			firstItem, ok := content[0].(map[string]interface{})
			require.True(t, ok)
			text, ok := firstItem["text"].(string)
			require.True(t, ok)

			// Try to parse as JSON to validate aggregation format
			var aggregated map[string]interface{}
			if err := json.Unmarshal([]byte(text), &aggregated); err != nil {
				t.Logf("%s returned plain text (acceptable)", toolName)
				return
			}

			// Verify standard aggregation fields
			if nodeCount, ok := aggregated["node_count"]; ok {
				t.Logf("%s aggregated from %v node(s)", toolName, nodeCount)
			}

			if status, ok := aggregated["status"].(string); ok {
				assert.Contains(t, []string{"success", "partial", "error"}, status,
					"%s: status must be valid", toolName)
			}
		})
	}
}

// TestNegative_ContextDeadlineExceeded verifies that the server
// handles context deadline/timeout gracefully without crashing
// or leaving connections in a broken state.
func TestNegative_ContextDeadlineExceeded(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	// Use a very short HTTP timeout to simulate context deadline
	shortClient := &http.Client{Timeout: 1 * time.Millisecond}

	params := map[string]interface{}{
		"name": "get_gpu_inventory",
	}
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      200,
		"method":  "tools/call",
		"params":  params,
	}

	body, err := json.Marshal(req)
	require.NoError(t, err)

	// This request should fail due to the extremely short timeout.
	// The important thing is that the client gets a clean error
	// (timeout or connection reset), not a panic.
	_, err = shortClient.Post(gatewayURL+"/mcp", "application/json",
		bytes.NewReader(body))

	// We expect an error (timeout). The test passes as long as the
	// client-side error is a clean timeout/deadline exceeded rather
	// than a protocol violation.
	if err != nil {
		t.Logf("Short timeout produced expected error: %v", err)
	} else {
		t.Log("Short timeout request completed (server was fast enough)")
	}

	// Now verify the server is still healthy after the timeout
	resp, err := testClient.Get(gatewayURL + "/healthz")
	require.NoError(t, err, "Server should still be healthy after client timeout")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"Health check should pass after client timeout")

	// Also verify the server can still process MCP requests normally
	normalResp := callTool(t, "get_gpu_inventory", nil)
	assert.Equal(t, "2.0", normalResp.JSONRPC,
		"Server should still respond to normal requests")
	assert.Nil(t, normalResp.Error,
		"Normal request should succeed after timeout recovery")
}

// TestNegative_ConcurrentErrorRequests verifies the server remains stable
// when receiving multiple simultaneous invalid requests.
func TestNegative_ConcurrentErrorRequests(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	const numRequests = 10

	type result struct {
		resp *JSONRPCResponse
		err  error
	}
	results := make(chan result, numRequests)

	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()

			// Alternate between different error scenarios
			var toolName string
			var args map[string]interface{}
			switch id % 3 {
			case 0:
				toolName = "nonexistent_tool_" + string(rune('A'+id))
				args = nil
			case 1:
				toolName = "describe_gpu_node"
				args = map[string]interface{}{
					"node_name": "fake-node-" + string(rune('0'+id)),
				}
			case 2:
				toolName = ""
				args = nil
			}

			params := map[string]interface{}{
				"name": toolName,
			}
			if args != nil {
				params["arguments"] = args
			}

			req := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      300 + id,
				"method":  "tools/call",
				"params":  params,
			}

			data, err := json.Marshal(req)
			if err != nil {
				results <- result{err: err}
				return
			}

			client := &http.Client{Timeout: 30 * time.Second}
			httpResp, err := client.Post(gatewayURL+"/mcp", "application/json",
				bytes.NewReader(data))
			if err != nil {
				results <- result{err: err}
				return
			}
			defer func() { _ = httpResp.Body.Close() }()

			respBody, err := io.ReadAll(httpResp.Body)
			if err != nil {
				results <- result{err: err}
				return
			}

			var rpcResp JSONRPCResponse
			if err := json.Unmarshal(respBody, &rpcResp); err != nil {
				results <- result{err: err}
				return
			}

			results <- result{resp: &rpcResp}
		}(i)
	}

	wg.Wait()
	close(results)

	validResponses := 0
	for r := range results {
		if r.err != nil {
			t.Logf("Request error (network-level): %v", r.err)
			continue
		}
		require.NotNil(t, r.resp,
			"Each response should be parseable JSON-RPC")
		assert.Equal(t, "2.0", r.resp.JSONRPC,
			"Each response must be JSON-RPC 2.0")
		validResponses++
	}

	assert.Equal(t, numRequests, validResponses,
		"All concurrent error requests should produce valid JSON-RPC responses")

	// Verify server is still healthy after the barrage
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	defer healthCancel()

	healthReq, err := http.NewRequestWithContext(healthCtx, http.MethodGet,
		gatewayURL+"/healthz", nil)
	require.NoError(t, err)

	healthResp, err := testClient.Do(healthReq)
	require.NoError(t, err,
		"Server must remain healthy after concurrent error requests")
	defer func() { _ = healthResp.Body.Close() }()
	assert.Equal(t, http.StatusOK, healthResp.StatusCode)
}

// TestNegative_MalformedToolArguments verifies the server handles
// malformed argument types gracefully (e.g., number where string expected).
func TestNegative_MalformedToolArguments(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
	}{
		{
			name:     "numeric node_name",
			toolName: "describe_gpu_node",
			args:     map[string]interface{}{"node_name": 12345},
		},
		{
			name:     "boolean node_name",
			toolName: "describe_gpu_node",
			args:     map[string]interface{}{"node_name": true},
		},
		{
			name:     "null node_name",
			toolName: "describe_gpu_node",
			args:     map[string]interface{}{"node_name": nil},
		},
		{
			name:     "array as argument",
			toolName: "get_gpu_inventory",
			args:     map[string]interface{}{"unexpected": []string{"a", "b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := callTool(t, tt.toolName, tt.args)

			assert.Equal(t, "2.0", resp.JSONRPC,
				"Must return valid JSON-RPC 2.0")

			// Server should not crash - either error or result is fine
			if resp.Error != nil {
				t.Logf("Error response (expected): code=%d, msg=%s",
					resp.Error.Code, resp.Error.Message)
				assert.NotEqual(t, -32603, resp.Error.Code,
					"Should not be internal server error")
			} else {
				require.NotNil(t, resp.Result)
				t.Logf("Tool handled malformed arg gracefully: %s",
					string(resp.Result))
			}
		})
	}
}

// TestNegative_EmptyRequestBody verifies the server handles an
// empty POST body without crashing.
func TestNegative_EmptyRequestBody(t *testing.T) {
	resp, err := testClient.Post(gatewayURL+"/mcp", "application/json",
		bytes.NewReader([]byte{}))
	require.NoError(t, err, "Server should accept the connection")
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	t.Logf("Empty body response: status=%d, body=%s",
		resp.StatusCode, string(body))

	// Server should return a JSON-RPC parse error, not crash
	if len(body) > 0 {
		var rpcResp JSONRPCResponse
		if err := json.Unmarshal(body, &rpcResp); err == nil {
			if rpcResp.Error != nil {
				assert.Equal(t, -32700, rpcResp.Error.Code,
					"Empty body should be parse error (-32700)")
			}
		}
	}
}

// TestNegative_OversizedRequestBody verifies the server handles
// unreasonably large request bodies without running out of memory.
func TestNegative_OversizedRequestBody(t *testing.T) {
	// Create a 1MB payload (well-formed JSON but oversized arguments)
	largeValue := make([]byte, 1024*1024)
	for i := range largeValue {
		largeValue[i] = 'A'
	}

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      400,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "get_gpu_inventory",
			"arguments": map[string]interface{}{
				"large_field": string(largeValue),
			},
		},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	t.Logf("Sending oversized request: %d bytes", len(data))

	resp, err := testClient.Post(gatewayURL+"/mcp", "application/json",
		bytes.NewReader(data))
	if err != nil {
		// Connection reset or refused is acceptable for oversized payloads
		t.Logf("Oversized request rejected at transport level: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	t.Logf("Oversized request response: status=%d, body_len=%d",
		resp.StatusCode, len(body))

	// Server should still be healthy
	healthResp, err := testClient.Get(gatewayURL + "/healthz")
	require.NoError(t, err,
		"Server must remain healthy after oversized request")
	defer func() { _ = healthResp.Body.Close() }()
	assert.Equal(t, http.StatusOK, healthResp.StatusCode)
}
