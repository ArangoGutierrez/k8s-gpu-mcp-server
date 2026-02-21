// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package gateway provides the MCP gateway router for multi-node GPU clusters.
package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/k8s"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/metrics"
	// Note: We use google/uuid instead of gateway.NewCorrelationID() to
	// generate RFC 4122 compliant UUIDs for request tracing. This ensures
	// compatibility with external distributed tracing systems (Jaeger,
	// Zipkin, OpenTelemetry) that expect standard UUID format for trace IDs.
	"github.com/google/uuid"
	"k8s.io/klog/v2"
)

// RoutingMode specifies how the gateway communicates with agents.
type RoutingMode string

const (
	// RoutingModeHTTP routes requests via HTTP to agent pods (recommended).
	RoutingModeHTTP RoutingMode = "http"
	// RoutingModeExec routes requests via kubectl exec (legacy).
	RoutingModeExec RoutingMode = "exec"
)

// DefaultMaxConcurrency is the default maximum concurrent requests to agents.
const DefaultMaxConcurrency = 10

// Router forwards MCP requests to node agents.
type Router struct {
	k8sClient      *k8s.Client
	httpClient     *AgentHTTPClient
	routingMode    RoutingMode
	circuitBreaker *CircuitBreaker
	maxConcurrency int
}

// RouterOption configures a Router.
type RouterOption func(*Router)

// WithRoutingMode sets the routing mode.
func WithRoutingMode(mode RoutingMode) RouterOption {
	return func(r *Router) {
		r.routingMode = mode
	}
}

// WithCircuitBreaker sets a custom circuit breaker.
func WithCircuitBreaker(cb *CircuitBreaker) RouterOption {
	return func(r *Router) {
		r.circuitBreaker = cb
	}
}

// WithMaxConcurrency sets the maximum number of concurrent requests to agents.
// This prevents memory exhaustion in large clusters. Default is 10.
func WithMaxConcurrency(n int) RouterOption {
	return func(r *Router) {
		if n > 0 {
			r.maxConcurrency = n
		}
	}
}

// NewRouter creates a new gateway router.
func NewRouter(k8sClient *k8s.Client, opts ...RouterOption) *Router {
	// Configure circuit breaker with metrics callback
	cbConfig := DefaultCircuitBreakerConfig()
	cbConfig.OnStateChange = func(node string, state int, healthy bool) {
		metrics.SetCircuitState(node, state)
		metrics.SetNodeHealth(node, healthy)
	}

	r := &Router{
		k8sClient:      k8sClient,
		httpClient:     NewAgentHTTPClient(),
		routingMode:    RoutingModeHTTP, // Default to HTTP
		circuitBreaker: NewCircuitBreaker(cbConfig),
		maxConcurrency: DefaultMaxConcurrency,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// NodeResult holds the result from a single node.
type NodeResult struct {
	NodeName string          `json:"node_name"`
	PodName  string          `json:"pod_name"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// RoutingMode returns the current routing mode.
func (r *Router) RoutingMode() RoutingMode {
	return r.routingMode
}

// RouteToNode sends an MCP request to a specific node's agent.
// This performs a pod lookup by node name first.
func (r *Router) RouteToNode(
	ctx context.Context,
	nodeName string,
	mcpRequest []byte,
) ([]byte, error) {
	requestID := uuid.New().String()
	klog.V(4).InfoS("routing to node",
		"requestID", requestID, "node", nodeName, "routingMode", r.routingMode)

	node, err := r.k8sClient.GetPodForNode(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("node not found: %w", err)
	}

	return r.routeToGPUNode(ctx, *node, mcpRequest, requestID)
}

// routeToGPUNode sends an MCP request to a known GPU node's agent.
// This is more efficient when the GPUNode is already known (e.g., from
// ListGPUNodes) as it avoids an extra API call.
func (r *Router) routeToGPUNode(
	ctx context.Context,
	node k8s.GPUNode,
	mcpRequest []byte,
	requestID string,
) ([]byte, error) {
	if !node.Ready {
		return nil, fmt.Errorf("agent on node %s is not ready", node.Name)
	}

	// Check circuit breaker before routing
	if !r.circuitBreaker.Allow(node.Name) {
		klog.V(2).InfoS("circuit open, skipping node",
			"requestID", requestID, "node", node.Name,
			"state", r.circuitBreaker.State(node.Name))
		return nil, fmt.Errorf("circuit open for node %s", node.Name)
	}

	startTime := time.Now()
	var response []byte
	var err error

	// Try HTTP routing if enabled
	if r.routingMode == RoutingModeHTTP {
		// Routing priority (intentional design):
		// 1. Pod IP (direct) - fastest, works when CNI is properly configured
		//    (e.g., Calico with VXLAN encapsulation for cross-node traffic)
		// 2. DNS endpoint (headless service) - fallback for environments where
		//    Pod IPs aren't directly routable but DNS resolution works
		// 3. Exec routing - last resort when network connectivity fails
		//
		// The Calico VXLAN fix (see docs/troubleshooting/cross-node-networking.md)
		// enables Pod IP routing to work across nodes, making it the preferred path.
		endpoint := node.GetAgentHTTPEndpoint()
		if endpoint == "" {
			// Fall back to DNS if Pod IP not available (pod still starting?)
			endpoint = node.GetAgentDNSEndpoint()
		}
		if endpoint != "" {
			response, err = r.routeViaHTTP(ctx, node, endpoint, mcpRequest,
				startTime, requestID)
		} else {
			klog.V(2).InfoS("pod has no IP, falling back to exec",
				"requestID", requestID, "node", node.Name, "pod", node.PodName)
			response, err = r.routeViaExec(ctx, node, mcpRequest,
				startTime, requestID)
		}
	} else {
		// Fall back to exec routing
		response, err = r.routeViaExec(ctx, node, mcpRequest, startTime, requestID)
	}

	// Record result with circuit breaker
	if err != nil {
		r.circuitBreaker.RecordFailure(node.Name)
		return nil, err
	}

	r.circuitBreaker.RecordSuccess(node.Name)
	return response, nil
}

// redactEndpoint replaces the host IP in an endpoint URL with a truncated
// SHA-256 hash (first 8 hex chars) for safe logging. This prevents internal
// pod IPs from leaking into non-debug logs while preserving enough
// information to correlate entries. Returns the original endpoint on parse error.
func redactEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "<redacted>"
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		hash := sha256.Sum256([]byte(host))
		u.Host = fmt.Sprintf("ip-%x:%s", hash[:4], u.Port())
		return u.String()
	}
	return endpoint
}

// routeViaHTTP sends request via HTTP to agent pod.
func (r *Router) routeViaHTTP(
	ctx context.Context,
	node k8s.GPUNode,
	endpoint string,
	mcpRequest []byte,
	startTime time.Time,
	requestID string,
) ([]byte, error) {
	klog.V(4).InfoS("routing via HTTP",
		"requestID", requestID, "node", node.Name, "endpoint", endpoint,
		"requestSize", len(mcpRequest))

	// For HTTP mode, we send just the tool call - no init framing needed
	// The agent HTTP server handles the full MCP session
	response, err := r.httpClient.CallMCP(ctx, endpoint, mcpRequest)
	duration := time.Since(startTime)

	// Use redacted endpoint in non-debug logs (V(0)-V(2)) to avoid
	// leaking internal pod IPs. Full endpoint is logged at V(3)+.
	safeEndpoint := redactEndpoint(endpoint)

	// Record metrics
	status := "success"
	if err != nil {
		status = "error"
		klog.ErrorS(err, "HTTP request failed",
			"requestID", requestID, "node", node.Name,
			"endpoint", safeEndpoint,
			"durationSeconds", duration.Seconds())
		klog.V(3).InfoS("HTTP request failed (debug)",
			"requestID", requestID, "node", node.Name,
			"endpoint", endpoint,
			"durationSeconds", duration.Seconds())
		metrics.RecordGatewayRequest(node.Name, "http", status, duration.Seconds())
		return nil, fmt.Errorf("HTTP request failed on node %s: %w", node.Name, err)
	}

	klog.InfoS("HTTP request completed",
		"requestID", requestID, "node", node.Name,
		"endpoint", safeEndpoint,
		"durationSeconds", duration.Seconds(), "responseBytes", len(response))
	klog.V(3).InfoS("HTTP request completed (debug)",
		"requestID", requestID, "node", node.Name,
		"endpoint", endpoint,
		"durationSeconds", duration.Seconds(), "responseBytes", len(response))

	metrics.RecordGatewayRequest(node.Name, "http", status, duration.Seconds())
	return response, nil
}

// routeViaExec sends request via kubectl exec to agent pod (legacy mode).
func (r *Router) routeViaExec(
	ctx context.Context,
	node k8s.GPUNode,
	mcpRequest []byte,
	startTime time.Time,
	requestID string,
) ([]byte, error) {
	klog.V(4).InfoS("routing via exec",
		"requestID", requestID, "node", node.Name, "pod", node.PodName,
		"requestSize", len(mcpRequest))

	stdin := bytes.NewReader(mcpRequest)
	response, err := r.k8sClient.ExecInPod(ctx, node.PodName, "agent", stdin)
	duration := time.Since(startTime)

	// Record metrics
	status := "success"
	if err != nil {
		status = "error"
		klog.ErrorS(err, "exec failed",
			"requestID", requestID, "node", node.Name, "pod", node.PodName,
			"durationSeconds", duration.Seconds())
		metrics.RecordGatewayRequest(node.Name, "exec", status, duration.Seconds())
		return nil, fmt.Errorf("exec failed on node %s: %w", node.Name, err)
	}

	klog.InfoS("exec completed",
		"requestID", requestID, "node", node.Name, "pod", node.PodName,
		"durationSeconds", duration.Seconds(), "responseBytes", len(response))

	metrics.RecordGatewayRequest(node.Name, "exec", status, duration.Seconds())
	return response, nil
}

// RouteToAllNodes sends an MCP request to all nodes and aggregates results.
// Returns partial success: results from healthy nodes even if some fail.
func (r *Router) RouteToAllNodes(
	ctx context.Context,
	mcpRequest []byte,
) ([]NodeResult, error) {
	requestID := uuid.New().String()
	startTime := time.Now()

	nodes, err := r.k8sClient.ListGPUNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list GPU nodes: %w", err)
	}

	// Count ready nodes for logging
	readyCount := 0
	for _, n := range nodes {
		if n.Ready {
			readyCount++
		}
	}

	klog.InfoS("routing to nodes",
		"requestID", requestID, "totalNodes", len(nodes),
		"readyNodes", readyCount, "routingMode", r.routingMode,
		"maxConcurrency", r.maxConcurrency)

	// Use channel for results instead of mutex-protected slice
	resultsCh := make(chan NodeResult, len(nodes))
	var wg sync.WaitGroup

	// Semaphore to limit concurrent requests
	sem := make(chan struct{}, r.maxConcurrency)

	skippedCount := 0

	for _, node := range nodes {
		// Check context between node iterations for prompt shutdown
		if ctx.Err() != nil {
			klog.V(2).InfoS("context cancelled, stopping node iteration",
				"requestID", requestID, "remaining", len(nodes))
			break
		}

		if !node.Ready {
			klog.V(2).InfoS("skipping unready node",
				"requestID", requestID, "node", node.Name)
			continue
		}

		// Check circuit breaker before spawning goroutine
		if !r.circuitBreaker.Allow(node.Name) {
			klog.V(2).InfoS("circuit open, skipping node",
				"requestID", requestID, "node", node.Name)
			skippedCount++

			resultsCh <- NodeResult{
				NodeName: node.Name,
				PodName:  node.PodName,
				Error: fmt.Sprintf("circuit open (state: %s)",
					r.circuitBreaker.State(node.Name)),
			}
			continue
		}

		wg.Add(1)
		go func(n k8s.GPUNode) {
			defer wg.Done()

			// Acquire semaphore (blocks if at max concurrency)
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }() // Release on completion
			case <-ctx.Done():
				resultsCh <- NodeResult{
					NodeName: n.Name,
					PodName:  n.PodName,
					Error:    "context cancelled while waiting for semaphore",
				}
				return
			}

			// Use routeToGPUNode directly to avoid redundant API call
			response, err := r.routeToGPUNode(ctx, n, mcpRequest, requestID)

			result := NodeResult{
				NodeName: n.Name,
				PodName:  n.PodName,
			}
			if err != nil {
				result.Error = err.Error()
			} else {
				result.Response = response
			}
			resultsCh <- result
		}(node)
	}

	// Close results channel after all goroutines complete
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	// Collect results
	results := make([]NodeResult, 0, len(nodes))
	successCount := 0
	failCount := 0

	for result := range resultsCh {
		results = append(results, result)
		if result.Error != "" {
			failCount++
		} else {
			successCount++
		}
	}

	totalDuration := time.Since(startTime)

	klog.InfoS("routing complete",
		"requestID", requestID, "totalNodes", len(nodes),
		"success", successCount, "failed", failCount, "skipped", skippedCount,
		"durationSeconds", totalDuration.Seconds())

	// Partial success: return results even if some failed.
	// Only return error if ALL nodes failed.
	if successCount == 0 && len(results) > 0 {
		return results, fmt.Errorf("all %d nodes failed", len(results))
	}

	return results, nil
}
