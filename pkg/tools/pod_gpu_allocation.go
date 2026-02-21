// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	// nvidiaGPUResource is the resource name for NVIDIA GPUs.
	nvidiaGPUResource = "nvidia.com/gpu"
	// gpuDeviceAnnotation is the annotation set by NVIDIA device plugin
	// containing assigned GPU UUIDs.
	gpuDeviceAnnotation = "nvidia.com/gpu.device"
)

// PodGPUAllocationHandler handles the get_pod_gpu_allocation tool.
type PodGPUAllocationHandler struct {
	clientset kubernetes.Interface
}

// NewPodGPUAllocationHandler creates a new pod GPU allocation handler.
func NewPodGPUAllocationHandler(clientset kubernetes.Interface) *PodGPUAllocationHandler {
	return &PodGPUAllocationHandler{
		clientset: clientset,
	}
}

// PodGPUAllocation represents GPU allocation for a pod.
type PodGPUAllocation struct {
	Name       string                   `json:"name"`
	Namespace  string                   `json:"namespace"`
	Status     string                   `json:"status"`
	Node       string                   `json:"node"`
	Containers []ContainerGPUAllocation `json:"containers"`
}

// ContainerGPUAllocation represents GPU allocation for a container.
type ContainerGPUAllocation struct {
	Name       string   `json:"name"`
	GPURequest int64    `json:"gpu_request"`
	GPULimit   int64    `json:"gpu_limit"`
	GPUUUIDs   []string `json:"gpu_uuids,omitempty"`
}

// PodGPUAllocationResponse is the response for get_pod_gpu_allocation.
// This struct is safe for concurrent read access; writes are confined to the
// handler goroutine that creates it.
type PodGPUAllocationResponse struct {
	APIVersion string             `json:"api_version"`
	Status     string             `json:"status"`
	NodeName string             `json:"node_name"`
	Pods     []PodGPUAllocation `json:"pods"`
	Summary  AllocationSummary  `json:"summary"`
	Error    string             `json:"error,omitempty"`
	Hint     string             `json:"hint,omitempty"`
}

// AllocationSummary provides summary statistics for GPU allocations.
type AllocationSummary struct {
	TotalPods          int   `json:"total_pods"`
	TotalGPUsAllocated int64 `json:"total_gpus_allocated"`
}

// Handle processes the get_pod_gpu_allocation tool request.
func (h *PodGPUAllocationHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("get_pod_gpu_allocation invoked")

	// Guard against nil clientset - this tool requires K8s access
	if h.clientset == nil {
		return mcp.NewToolResultError(
			"K8s client not configured - this tool requires cluster access"), nil
	}

	args := request.GetArguments()

	// Extract node_name (required)
	nodeName, ok := args["node_name"].(string)
	if !ok || nodeName == "" {
		return mcp.NewToolResultError("node_name is required"), nil
	}

	// Validate node name format
	if !isValidNodeName(nodeName) {
		return mcp.NewToolResultError(
			"invalid node_name: must be a valid DNS subdomain (RFC 1123)"), nil
	}

	// Extract namespace (optional, empty means all namespaces)
	namespace := ""
	if ns, ok := args["namespace"].(string); ok {
		namespace = ns
	}

	klog.V(4).InfoS("querying pods", "node", nodeName, "namespace", namespace)

	// List pods on the specified node
	listOpts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	}

	pods, err := h.clientset.CoreV1().Pods(namespace).List(ctx, listOpts)
	if err != nil {
		klog.V(2).ErrorS(err, "failed to list pods",
			"node", nodeName, "namespace", namespace,
			"hint", "Agent may lack RBAC permissions. Apply deployment/rbac/agent-rbac-readonly.yaml")
		// Return sanitized error to client — avoid leaking internal K8s API details
		response := PodGPUAllocationResponse{
			APIVersion: APIVersion,
			Status:     "error",
			NodeName:   nodeName,
			Pods:       []PodGPUAllocation{},
			Summary:    AllocationSummary{},
			Error:      "internal error: failed to list pods on node",
			Hint:       "Check agent RBAC permissions. See server logs for details.",
		}
		jsonBytes, marshalErr := json.MarshalIndent(response, "", "  ")
		if marshalErr != nil {
			return mcp.NewToolResultError(
				fmt.Sprintf("failed to list pods: %s", err)), nil
		}
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	// Process pods and filter for GPU allocations
	// Note: Also filter by nodeName client-side since fake clientset
	// doesn't support FieldSelector in tests
	gpuPods := make([]PodGPUAllocation, 0)
	var totalGPUs int64

	for _, pod := range pods.Items {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			klog.V(2).InfoS("context cancelled during pod enumeration",
				"node", nodeName, "error", ctx.Err())
			return mcp.NewToolResultError("operation cancelled"), nil
		default:
		}

		// Client-side node filter (FieldSelector backup for fake clients)
		if pod.Spec.NodeName != nodeName {
			continue
		}

		allocation := h.extractGPUAllocation(&pod)
		if allocation != nil {
			gpuPods = append(gpuPods, *allocation)
			for _, container := range allocation.Containers {
				totalGPUs += container.GPURequest
			}
		}
	}

	// Create response
	response := PodGPUAllocationResponse{
		APIVersion: APIVersion,
		Status:     "success",
		NodeName:   nodeName,
		Pods:       gpuPods,
		Summary: AllocationSummary{
			TotalPods:          len(gpuPods),
			TotalGPUsAllocated: totalGPUs,
		},
	}

	// Marshal to JSON
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response", "node", nodeName)
		return mcp.NewToolResultError(
			"internal error: failed to format response"), nil
	}

	klog.InfoS("get_pod_gpu_allocation completed",
		"node", nodeName, "pods", len(gpuPods), "gpus", totalGPUs)

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// extractGPUAllocation extracts GPU allocation info from a pod.
// Returns nil if the pod has no GPU allocations.
func (h *PodGPUAllocationHandler) extractGPUAllocation(
	pod *corev1.Pod,
) *PodGPUAllocation {
	containers := make([]ContainerGPUAllocation, 0)
	hasGPU := false

	for _, container := range pod.Spec.Containers {
		gpuRequest := int64(0)
		gpuLimit := int64(0)

		if req, ok := container.Resources.Requests[nvidiaGPUResource]; ok {
			gpuRequest = req.Value()
			hasGPU = true
		}
		if lim, ok := container.Resources.Limits[nvidiaGPUResource]; ok {
			gpuLimit = lim.Value()
			hasGPU = true
		}

		if gpuRequest > 0 || gpuLimit > 0 {
			containerAlloc := ContainerGPUAllocation{
				Name:       container.Name,
				GPURequest: gpuRequest,
				GPULimit:   gpuLimit,
			}
			containers = append(containers, containerAlloc)
		}
	}

	if !hasGPU {
		return nil
	}

	allocation := &PodGPUAllocation{
		Name:       pod.Name,
		Namespace:  pod.Namespace,
		Status:     string(pod.Status.Phase),
		Node:       pod.Spec.NodeName,
		Containers: containers,
	}

	// Extract GPU UUIDs from device plugin annotation.
	// Note: The NVIDIA device plugin sets this annotation at the pod level,
	// not per-container. For multi-container pods, we cannot determine which
	// specific GPUs are assigned to which container from the annotation alone.
	// We assign all UUIDs to the first container with GPU requests as a
	// best-effort approximation. For accurate per-container GPU mapping,
	// inspect the container's environment variables (NVIDIA_VISIBLE_DEVICES).
	if uuids, ok := pod.Annotations[gpuDeviceAnnotation]; ok && uuids != "" {
		gpuUUIDs := strings.Split(uuids, ",")
		for i := range allocation.Containers {
			if allocation.Containers[i].GPURequest > 0 {
				allocation.Containers[i].GPUUUIDs = gpuUUIDs
				break
			}
		}
	}

	return allocation
}

// GetPodGPUAllocationTool returns the MCP tool definition.
func GetPodGPUAllocationTool() mcp.Tool {
	return mcp.NewTool("get_pod_gpu_allocation",
		mcp.WithDescription(
			"Shows GPU allocation for pods on a specific node. "+
				"Returns pods with nvidia.com/gpu resource requests, "+
				"including GPU UUIDs assigned to each container via "+
				"the NVIDIA device plugin annotations.",
		),
		mcp.WithString("node_name",
			mcp.Required(),
			mcp.Description("Node name to query"),
		),
		mcp.WithString("namespace",
			mcp.Description("Namespace filter (optional, default: all namespaces)"),
		),
	)
}
