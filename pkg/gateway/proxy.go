// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/k8s"
	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

// DefaultRateLimit is the default number of tool call requests per second.
const DefaultRateLimit = 100

// DefaultRateBurst is the default burst size for the rate limiter.
const DefaultRateBurst = 20

// ProxyHandler forwards tool calls to node agents and aggregates responses.
type ProxyHandler struct {
	router   *Router
	toolName string
	limiter  *rate.Limiter
}

// ProxyOption configures a ProxyHandler.
type ProxyOption func(*ProxyHandler)

// WithRateLimit sets the rate limiter for the proxy handler.
// rps is the requests per second and burst is the maximum burst size.
func WithRateLimit(rps float64, burst int) ProxyOption {
	return func(p *ProxyHandler) {
		p.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}
}

// NewProxyHandler creates a handler that proxies a specific tool to agents.
func NewProxyHandler(
	k8sClient *k8s.Client,
	toolName string,
	opts ...RouterOption,
) *ProxyHandler {
	return &ProxyHandler{
		router:   NewRouter(k8sClient, opts...),
		toolName: toolName,
		limiter:  rate.NewLimiter(DefaultRateLimit, DefaultRateBurst),
	}
}

// NewProxyHandlerWithOptions creates a handler with proxy-level options.
func NewProxyHandlerWithOptions(
	k8sClient *k8s.Client,
	toolName string,
	proxyOpts []ProxyOption,
	routerOpts ...RouterOption,
) *ProxyHandler {
	p := &ProxyHandler{
		router:   NewRouter(k8sClient, routerOpts...),
		toolName: toolName,
		limiter:  rate.NewLimiter(DefaultRateLimit, DefaultRateBurst),
	}
	for _, opt := range proxyOpts {
		opt(p)
	}
	return p
}

// Handle proxies the tool call to all node agents and aggregates results.
func (p *ProxyHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	// Generate correlation ID if not present
	correlationID := CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = NewCorrelationID()
		ctx = WithCorrelationID(ctx, correlationID)
	}

	klog.InfoS("proxy_tool invoked",
		"tool", p.toolName,
		"routingMode", p.router.RoutingMode(),
		"correlationID", correlationID)

	// Check rate limit
	if p.limiter != nil && !p.limiter.Allow() {
		klog.V(2).InfoS("rate limit exceeded",
			"tool", p.toolName, "correlationID", correlationID)
		return mcp.NewToolResultError(fmt.Sprintf(
			"rate limit exceeded for tool %q: too many requests, retry later",
			p.toolName)), nil
	}

	// Extract include_k8s_metadata parameter (default: true for gateway mode)
	includeK8sMetadata := true
	if args := request.GetArguments(); args != nil {
		if v, ok := args["include_k8s_metadata"].(bool); ok {
			includeK8sMetadata = v
		}
	}

	var mcpRequest []byte
	var err error

	if p.router.RoutingMode() == RoutingModeHTTP {
		// HTTP mode: Build single tool call request (no init needed)
		mcpRequest, err = BuildHTTPToolRequest(
			p.toolName, request.GetArguments())
	} else {
		// Exec mode: Build init + tool framing for oneshot agents
		mcpRequest, err = BuildMCPRequest(p.toolName, request.GetArguments())
	}

	if err != nil {
		klog.ErrorS(err, "failed to build MCP request", "tool", p.toolName)
		return mcp.NewToolResultError(
			fmt.Sprintf("tool %q: failed to build request: %v",
				p.toolName, err)), nil
	}

	// Route to all nodes
	results, err := p.router.RouteToAllNodes(ctx, mcpRequest)
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("tool %q: failed to route to nodes: %v",
				p.toolName, err)), nil
	}

	// Aggregate results (parsing differs by mode)
	aggregated := p.aggregateResults(ctx, results, includeK8sMetadata)

	jsonBytes, err := json.MarshalIndent(aggregated, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("tool %q: failed to marshal response: %v",
				p.toolName, err)), nil
	}

	klog.InfoS("proxy_tool completed",
		"tool", p.toolName, "nodeCount", len(results), "correlationID", correlationID)

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// aggregateResults combines results from multiple nodes.
func (p *ProxyHandler) aggregateResults(
	ctx context.Context,
	results []NodeResult,
	includeK8sMetadata bool,
) interface{} {
	// Check for API version mismatches across agent responses
	p.checkAPIVersionMismatch(results)

	// Special handling for get_gpu_inventory - create cluster summary
	if p.toolName == "get_gpu_inventory" {
		return p.aggregateGPUInventory(ctx, results, includeK8sMetadata)
	}

	// Default aggregation for other tools
	return p.aggregateDefault(results)
}

// checkAPIVersionMismatch logs a warning if agent responses report different
// api_version values. This is informational only and does NOT fail the request.
func (p *ProxyHandler) checkAPIVersionMismatch(results []NodeResult) {
	versions := make(map[string][]string) // version -> list of node names
	for _, result := range results {
		if result.Error != "" {
			continue
		}
		parsed := parseToolResponse(result.Response)
		if m, ok := parsed.(map[string]interface{}); ok {
			if v, ok := m["api_version"].(string); ok && v != "" {
				versions[v] = append(versions[v], result.NodeName)
			}
		}
	}
	if len(versions) > 1 {
		klog.InfoS("API version mismatch detected across agents",
			"tool", p.toolName,
			"versions", versions)
	}
}

// aggregateDefault provides the standard aggregation for most tools.
func (p *ProxyHandler) aggregateDefault(results []NodeResult) interface{} {
	aggregated := map[string]interface{}{
		"status":     "success",
		"node_count": len(results),
		"nodes":      []interface{}{},
	}

	successCount := 0
	errorCount := 0
	nodeResults := make([]interface{}, 0, len(results))

	for _, result := range results {
		nodeData := map[string]interface{}{
			"node_name": result.NodeName,
			"pod_name":  result.PodName,
		}

		if result.Error != "" {
			nodeData["error"] = result.Error
			errorCount++
		} else {
			parsed := parseToolResponse(result.Response)
			nodeData["data"] = parsed
			successCount++
		}

		nodeResults = append(nodeResults, nodeData)
	}

	aggregated["nodes"] = nodeResults
	aggregated["success_count"] = successCount
	aggregated["error_count"] = errorCount

	if errorCount > 0 && successCount == 0 {
		aggregated["status"] = "error"
	} else if errorCount > 0 {
		aggregated["status"] = "partial"
	}

	return aggregated
}

// aggregateGPUInventory creates a cluster-wide GPU inventory with summary.
// Enriches node data with K8s metadata when the client is available.
func (p *ProxyHandler) aggregateGPUInventory(
	ctx context.Context,
	results []NodeResult,
	includeK8sMetadata bool,
) interface{} {
	totalGPUs := 0
	readyNodes := 0
	gpuTypes := make(map[string]bool)
	nodes := make([]interface{}, 0, len(results))

	// Track cluster-level GPU resources
	var clusterCapacity, clusterAllocatable, clusterAllocated int64

	for _, result := range results {
		nodeData := map[string]interface{}{
			"name": result.NodeName,
		}

		if result.Error != "" {
			nodeData["status"] = "error"
			nodeData["error"] = result.Error
		} else {
			nodeData["status"] = "ready"
			readyNodes++

			// Parse the inventory response
			parsed := parseToolResponse(result.Response)
			if inv, ok := parsed.(map[string]interface{}); ok {
				// Extract driver/cuda versions
				if v, ok := inv["driver_version"]; ok {
					nodeData["driver_version"] = v
				}
				if v, ok := inv["cuda_version"]; ok {
					nodeData["cuda_version"] = v
				}

				// Extract and flatten GPU list
				if devices, ok := inv["devices"].([]interface{}); ok {
					totalGPUs += len(devices)
					gpus := make([]interface{}, 0, len(devices))
					for _, d := range devices {
						if dev, ok := d.(map[string]interface{}); ok {
							// Collect GPU types
							if name, ok := dev["name"].(string); ok {
								gpuTypes[name] = true
							}
							// Flatten memory to memory_total_gb
							gpu := flattenGPUInfo(dev)
							gpus = append(gpus, gpu)
						} else {
							klog.V(3).InfoS(
								"type assertion failed for GPU device entry",
								"node", result.NodeName,
								"type", fmt.Sprintf("%T", d))
						}
					}
					nodeData["gpus"] = gpus
				} else {
					klog.V(3).InfoS(
						"type assertion failed for devices field",
						"node", result.NodeName,
						"type", fmt.Sprintf("%T", inv["devices"]))
				}
			} else {
				klog.V(3).InfoS(
					"type assertion failed for inventory response",
					"node", result.NodeName,
					"type", fmt.Sprintf("%T", parsed))
			}
		}

		// Enrich with K8s metadata if requested and client available
		if includeK8sMetadata && p.router.k8sClient != nil {
			nodeName, ok := nodeData["name"].(string)
			if ok && nodeName != "" {
				metadata, err := p.getNodeK8sMetadata(ctx, nodeName)
				if err != nil {
					klog.V(4).InfoS("failed to get K8s metadata",
						"node", nodeName, "error", err)
				} else {
					nodeData["kubernetes"] = metadata

					// Accumulate cluster-level GPU resources
					if metadata.GPUResources != nil {
						clusterCapacity += metadata.GPUResources.Capacity
						clusterAllocatable += metadata.GPUResources.Allocatable
						clusterAllocated += metadata.GPUResources.Allocated
					}
				}
			}
		}

		nodes = append(nodes, nodeData)
	}

	// Build GPU types list (sorted for deterministic output)
	types := make([]string, 0, len(gpuTypes))
	for t := range gpuTypes {
		types = append(types, t)
	}
	sort.Strings(types)

	// Build cluster summary
	clusterSummary := map[string]interface{}{
		"total_nodes": len(results),
		"ready_nodes": readyNodes,
		"total_gpus":  totalGPUs,
		"gpu_types":   types,
	}

	// Add GPU resource counts if K8s metadata was included
	if includeK8sMetadata && p.router.k8sClient != nil {
		clusterSummary["gpus_capacity"] = clusterCapacity
		clusterSummary["gpus_allocatable"] = clusterAllocatable
		clusterSummary["gpus_allocated"] = clusterAllocated
		clusterSummary["gpus_available"] = clusterAllocatable - clusterAllocated
	}

	return map[string]interface{}{
		"status":          "success",
		"cluster_summary": clusterSummary,
		"nodes":           nodes,
	}
}

// flattenGPUInfo simplifies GPU info for cluster view.
// Returns a flattened GPU info map with proper nil handling.
func flattenGPUInfo(dev map[string]interface{}) map[string]interface{} {
	if dev == nil {
		return map[string]interface{}{"error": "nil device data"}
	}

	gpu := make(map[string]interface{})

	// Copy basic fields with nil checks
	if v, ok := dev["index"]; ok {
		gpu["index"] = v
	}
	if v, ok := dev["name"]; ok {
		gpu["name"] = v
	}
	if v, ok := dev["uuid"]; ok {
		gpu["uuid"] = v
	}

	// Flatten memory with proper type checking
	if mem, ok := dev["memory"].(map[string]interface{}); ok && mem != nil {
		if total, ok := mem["total_bytes"].(float64); ok {
			gpu["memory_total_gb"] = total / (1024 * 1024 * 1024)
		}
	}

	// Flatten temperature with proper type checking
	if temp, ok := dev["temperature"].(map[string]interface{}); ok && temp != nil {
		if curr, ok := temp["current_celsius"].(float64); ok {
			gpu["temperature_c"] = int(curr)
		}
	}

	// Flatten utilization with proper type checking
	if util, ok := dev["utilization"].(map[string]interface{}); ok && util != nil {
		if gpuPct, ok := util["gpu_percent"].(float64); ok {
			gpu["utilization_percent"] = int(gpuPct)
		}
	}

	return gpu
}

// NodeK8sMetadata contains Kubernetes node information.
type NodeK8sMetadata struct {
	Labels       map[string]string `json:"labels,omitempty"`
	Conditions   map[string]bool   `json:"conditions,omitempty"`
	GPUResources *GPUResourceInfo  `json:"gpu_resources,omitempty"`
}

// GPUResourceInfo contains GPU resource capacity and allocation.
type GPUResourceInfo struct {
	Capacity    int64 `json:"capacity"`
	Allocatable int64 `json:"allocatable"`
	Allocated   int64 `json:"allocated"`
}

// getNodeK8sMetadata fetches K8s node information.
func (p *ProxyHandler) getNodeK8sMetadata(
	ctx context.Context,
	nodeName string,
) (*NodeK8sMetadata, error) {
	node, err := p.router.k8sClient.GetNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}

	// Filter to GPU-relevant labels
	labels := filterGPULabels(node.Labels)

	// Extract conditions as bool map
	conditions := make(map[string]bool)
	for _, cond := range node.Status.Conditions {
		conditions[string(cond.Type)] = cond.Status == corev1.ConditionTrue
	}

	// Get GPU resource info
	gpuResources := &GPUResourceInfo{}
	if qty, ok := node.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]; ok {
		gpuResources.Capacity = qty.Value()
	}
	if qty, ok := node.Status.Allocatable[corev1.ResourceName("nvidia.com/gpu")]; ok {
		gpuResources.Allocatable = qty.Value()
	}

	// Get accurate GPU allocation from pods
	allocated, err := p.getNodeGPUAllocation(ctx, nodeName)
	if err != nil {
		klog.V(4).InfoS("failed to get GPU allocation, using fallback",
			"node", nodeName, "error", err)
		// Fall back to capacity - allocatable
		allocated = gpuResources.Capacity - gpuResources.Allocatable
	}
	gpuResources.Allocated = allocated

	return &NodeK8sMetadata{
		Labels:       labels,
		Conditions:   conditions,
		GPUResources: gpuResources,
	}, nil
}

// getNodeGPUAllocation returns the number of GPUs allocated on a node.
// Queries pods across all namespaces for accurate cluster-wide allocation counts.
// Requires ClusterRole with pods: list permission (granted to gateway by default).
func (p *ProxyHandler) getNodeGPUAllocation(
	ctx context.Context,
	nodeName string,
) (int64, error) {
	// List pods on this node across all namespaces for accurate allocation count
	pods, err := p.router.k8sClient.ListPodsAllNamespaces(ctx,
		"", // all labels
		fmt.Sprintf("spec.nodeName=%s", nodeName))
	if err != nil {
		return 0, err
	}

	var totalAllocated int64
	for _, pod := range pods {
		// Skip completed/failed pods
		if pod.Status.Phase == corev1.PodSucceeded ||
			pod.Status.Phase == corev1.PodFailed {
			continue
		}

		for _, container := range pod.Spec.Containers {
			if req, ok := container.Resources.Requests[corev1.ResourceName(
				"nvidia.com/gpu")]; ok {
				totalAllocated += req.Value()
			}
		}
	}

	return totalAllocated, nil
}

// filterGPULabels returns labels relevant to GPU operations.
func filterGPULabels(labels map[string]string) map[string]string {
	relevantPrefixes := []string{
		"nvidia.com/",
		"topology.kubernetes.io/",
		"node.kubernetes.io/instance-type",
		"kubernetes.io/arch",
		"kubernetes.io/os",
	}
	relevantExact := []string{
		"gpu-type",
		"accelerator",
	}

	filtered := make(map[string]string)
	for k, v := range labels {
		// Check prefix matches
		for _, prefix := range relevantPrefixes {
			if strings.HasPrefix(k, prefix) {
				filtered[k] = v
				break
			}
		}
		// Check exact matches
		for _, exact := range relevantExact {
			if k == exact {
				filtered[k] = v
				break
			}
		}
	}
	return filtered
}

// parseToolResponse extracts the tool result from the MCP response.
// Uses the framing utilities for robust JSON parsing.
// This function handles both stdio (multi-line) and HTTP (single-line) responses.
func parseToolResponse(response []byte) interface{} {
	// Try HTTP mode first (single JSON object)
	data, err := ParseHTTPResponse(response)
	if err == nil {
		if data == nil {
			return nil
		}
		return data
	}

	// Fall back to stdio mode (multi-line response)
	data, err = ParseStdioResponse(response)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	if data == nil {
		return nil
	}

	return data
}
