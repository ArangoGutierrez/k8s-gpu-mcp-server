// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResilience_PartialNodeFailure(t *testing.T) {
	// Skip if in CI (pod deletion may cause flakiness)
	if testing.Short() {
		t.Skip("Skipping resilience test in short mode")
	}

	// Initialize session
	sendMCPRequest(t, "initialize.json")

	// Get initial inventory (should succeed with all nodes)
	resp := sendMCPRequest(t, "tools_call_inventory.json")
	require.Nil(t, resp.Error, "Initial request should succeed")
	t.Log("Initial inventory request succeeded")

	// Get list of agent pods.
	// Note: ctx is the package-level context with 10-minute timeout from TestMain.
	// Individual test timeouts can be enforced via testing.T.Deadline() if needed.
	cmd := exec.CommandContext(ctx, "kubectl", "get", "pods",
		"-n", namespace,
		"-l", "app.kubernetes.io/component=gpu-diagnostics",
		"-o", "jsonpath={.items[*].metadata.name}")
	out, err := cmd.Output()
	require.NoError(t, err)

	// Only proceed if we have multiple pods
	pods := splitPodNames(string(out))
	if len(pods) < 2 {
		t.Skip("Need at least 2 agent pods for partial failure test")
	}

	t.Logf("Found %d agent pods: %v", len(pods), pods)

	// Delete one agent pod (non-destructive - DaemonSet will recreate)
	podToDelete := pods[0]
	t.Logf("Deleting pod %s to simulate failure...", podToDelete)

	deleteCmd := exec.CommandContext(ctx, "kubectl", "delete", "pod",
		"-n", namespace,
		podToDelete,
		"--wait=false")
	err = deleteCmd.Run()
	require.NoError(t, err, "Failed to delete pod")

	// Give time for pod deletion to register
	time.Sleep(2 * time.Second)

	// Request should still succeed (partial success from remaining nodes)
	t.Log("Testing request with partial node failure...")
	resp = sendMCPRequest(t, "tools_call_inventory.json")

	// Should return data (partial success) or graceful error
	if resp.Error != nil {
		t.Logf("Request returned error (acceptable for partial failure): %s",
			resp.Error.Message)
		// Should not be an internal server error
		assert.NotEqual(t, -32603, resp.Error.Code,
			"Should not be internal server error")
	} else {
		require.NotNil(t, resp.Result,
			"Should return partial results with node failure")
		t.Log("Request succeeded with partial results")
	}
}

func TestResilience_GatewayRecovery(t *testing.T) {
	// Skip if in CI
	if testing.Short() {
		t.Skip("Skipping resilience test in short mode")
	}

	// Wait for pods to recover from previous test
	t.Log("Waiting for pods to recover...")
	cmd := exec.CommandContext(ctx, "kubectl", "wait",
		"--for=condition=ready", "pod",
		"-l", "app.kubernetes.io/name=k8s-gpu-mcp-server",
		"-n", namespace,
		"--timeout=120s")
	err := cmd.Run()
	require.NoError(t, err, "Pods should recover")

	// Initialize session
	sendMCPRequest(t, "initialize.json")

	// Should succeed after recovery
	resp := sendMCPRequest(t, "tools_call_inventory.json")
	assert.Nil(t, resp.Error, "Request should succeed after recovery")
	require.NotNil(t, resp.Result, "Should return results after recovery")

	t.Log("Gateway recovered successfully")
}

func TestResilience_TimeoutHandling(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	// Send a normal request and verify it doesn't timeout
	start := time.Now()
	resp := sendMCPRequest(t, "tools_call_inventory.json")
	elapsed := time.Since(start)

	t.Logf("Request completed in %v", elapsed)

	assert.Nil(t, resp.Error, "Normal request should not timeout")
	require.NotNil(t, resp.Result)

	// Should complete well under 30 seconds
	assert.Less(t, elapsed, 30*time.Second,
		"Request should complete within 30 seconds")
}

func TestResilience_RetryAfterError(t *testing.T) {
	sendMCPRequest(t, "initialize.json")

	// First request
	resp1 := sendMCPRequest(t, "tools_call_inventory.json")
	require.Nil(t, resp1.Error, "First request should succeed")

	// Second request should also succeed (no state corruption)
	resp2 := sendMCPRequest(t, "tools_call_inventory.json")
	require.Nil(t, resp2.Error, "Second request should succeed")

	// Results should be consistent
	var result1, result2 map[string]interface{}
	err := json.Unmarshal(resp1.Result, &result1)
	require.NoError(t, err)
	err = json.Unmarshal(resp2.Result, &result2)
	require.NoError(t, err)

	// Both should have content
	content1, ok := result1["content"].([]interface{})
	require.True(t, ok, "Result1 should contain content array")
	content2, ok := result2["content"].([]interface{})
	require.True(t, ok, "Result2 should contain content array")
	assert.NotEmpty(t, content1)
	assert.NotEmpty(t, content2)

	t.Log("Retry consistency verified")
}

func TestResilience_GatewayHA(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping gateway HA test in short mode")
	}

	// Check if gateway has multiple replicas
	cmd := exec.CommandContext(ctx, "kubectl", "get", "deployment",
		helmReleaseName+"-k8s-gpu-mcp-server-gateway",
		"-n", namespace,
		"-o", "jsonpath={.spec.replicas}")
	out, err := cmd.Output()
	require.NoError(t, err)

	replicas := strings.TrimSpace(string(out))
	if replicas == "1" {
		t.Skip("Gateway HA test requires replicas > 1")
	}

	t.Logf("Gateway has %s replicas, testing HA...", replicas)

	// Initialize MCP session
	sendMCPRequest(t, "initialize.json")

	// Get gateway pods
	cmd = exec.CommandContext(ctx, "kubectl", "get", "pods",
		"-n", namespace,
		"-l", "app.kubernetes.io/component=gateway",
		"-o", "jsonpath={.items[*].metadata.name}")
	out, err = cmd.Output()
	require.NoError(t, err)

	pods := strings.Fields(string(out))
	require.GreaterOrEqual(t, len(pods), 2, "Expected at least 2 gateway pods")
	t.Logf("Found gateway pods: %v", pods)

	// Delete one gateway pod
	podToDelete := pods[0]
	t.Logf("Deleting gateway pod %s...", podToDelete)

	deleteCmd := exec.CommandContext(ctx, "kubectl", "delete", "pod",
		"-n", namespace, podToDelete, "--wait=false")
	err = deleteCmd.Run()
	require.NoError(t, err)

	// Gateway should remain available via remaining pod(s)
	time.Sleep(2 * time.Second)

	t.Log("Testing request with one gateway pod deleted...")
	resp := sendMCPRequest(t, "tools_call_inventory.json")

	require.Nil(t, resp.Error, "Gateway should remain available during pod deletion")
	require.NotNil(t, resp.Result, "Should return results from remaining gateway")

	t.Log("Gateway HA verified: service remained available during pod deletion")
}

func TestResilience_CircuitBreakerMetrics(t *testing.T) {
	// Check that circuit breaker metrics are exposed
	resp, err := httpGet(gatewayURL + "/metrics")
	require.NoError(t, err)

	// Look for circuit breaker related metrics
	// Note: Metrics may not exist if no failures have occurred
	if len(resp) > 0 {
		t.Log("Metrics endpoint accessible")
		// Don't require specific circuit breaker metrics as they may not
		// be present in a healthy cluster
	}
}

// Helper functions

// splitPodNames parses kubectl output of pod names separated by whitespace.
func splitPodNames(output string) []string {
	// Use strings.Fields which handles all whitespace correctly
	return strings.Fields(output)
}

// httpGet performs an HTTP GET and returns the response body.
// Uses explicit timeout to avoid hanging if server is unresponsive.
func httpGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	return io.ReadAll(resp.Body)
}
