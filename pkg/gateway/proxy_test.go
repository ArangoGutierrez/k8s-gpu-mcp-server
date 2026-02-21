// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/k8s"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildMCPRequest_Proxy(t *testing.T) {
	args := map[string]interface{}{"filter": "healthy"}
	request, err := BuildMCPRequest("get_gpu_health", args)
	require.NoError(t, err, "BuildMCPRequest should not error")

	// Should contain two JSON objects
	lines := SplitJSONObjects(request)
	require.Len(t, lines, 2, "expected 2 JSON objects")

	// First should be initialize
	var init map[string]interface{}
	err = json.Unmarshal(lines[0], &init)
	require.NoError(t, err, "failed to parse init request")
	assert.Equal(t, "initialize", init["method"])

	// Second should be tools/call
	var tool map[string]interface{}
	err = json.Unmarshal(lines[1], &tool)
	require.NoError(t, err, "failed to parse tool request")
	assert.Equal(t, "tools/call", tool["method"])

	// Verify tool name in params
	params, ok := tool["params"].(map[string]interface{})
	require.True(t, ok, "params is not a map")
	assert.Equal(t, "get_gpu_health", params["name"])
}

func TestBuildMCPRequest_NilArgumentsProxy(t *testing.T) {
	request, err := BuildMCPRequest("get_gpu_inventory", nil)
	require.NoError(t, err)

	lines := SplitJSONObjects(request)
	require.Len(t, lines, 2)

	var tool map[string]interface{}
	err = json.Unmarshal(lines[1], &tool)
	require.NoError(t, err)

	params := tool["params"].(map[string]interface{})
	assert.Equal(t, "get_gpu_inventory", params["name"])
	assert.Nil(t, params["arguments"])
}

func TestParseToolResponse(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		wantErr     bool
		wantContent bool
		wantStatus  string
	}{
		{
			name: "valid response with JSON content",
			response: `{"jsonrpc":"2.0","id":0,"result":{}}` +
				`{"jsonrpc":"2.0","id":1,"result":{"content":[` +
				`{"type":"text","text":"{\"status\":\"healthy\"}"}]}}`,
			wantErr:     false,
			wantContent: true,
			wantStatus:  "healthy",
		},
		{
			name: "error response",
			response: `{"jsonrpc":"2.0","id":0,"result":{}}` +
				`{"jsonrpc":"2.0","id":1,"error":{"code":-1,` +
				`"message":"tool failed"}}`,
			wantErr:     true,
			wantContent: false,
		},
		{
			name: "isError true response",
			response: `{"jsonrpc":"2.0","id":0,"result":{}}` +
				`{"jsonrpc":"2.0","id":1,"result":{"content":[` +
				`{"type":"text","text":"something went wrong"}],` +
				`"isError":true}}`,
			wantErr:     true,
			wantContent: false,
		},
		{
			name:        "empty response",
			response:    "",
			wantErr:     true,
			wantContent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseToolResponse([]byte(tt.response))
			resultMap, isMap := result.(map[string]interface{})

			if tt.wantErr {
				require.True(t, isMap, "expected map result")
				assert.NotNil(t, resultMap["error"], "expected error in result")
			} else if tt.wantContent {
				require.True(t, isMap, "expected map result")
				assert.Nil(t, resultMap["error"], "unexpected error")
				assert.Equal(t, tt.wantStatus, resultMap["status"])
			}
		})
	}
}

func TestSplitJSONObjects_Proxy(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "three simple objects",
			input:    `{"a":1}{"b":2}{"c":3}`,
			expected: 3,
		},
		{
			name:     "nested objects",
			input:    `{"a":{"nested":1}}{"b":2}`,
			expected: 2,
		},
		{
			name:     "with newlines",
			input:    "{\"a\":1}\n{\"b\":2}",
			expected: 2,
		},
		{
			name:     "empty",
			input:    "",
			expected: 0,
		},
		{
			name:     "single object",
			input:    `{"key":"value"}`,
			expected: 1,
		},
		{
			name:     "deeply nested",
			input:    `{"a":{"b":{"c":{"d":1}}}}`,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := SplitJSONObjects([]byte(tt.input))
			assert.Len(t, lines, tt.expected)
		})
	}
}

func TestAggregateResults_AllSuccess(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "test",
		router:   &Router{},
	}

	results := []NodeResult{
		{
			NodeName: "node-1",
			PodName:  "pod-1",
			Response: []byte(`{"jsonrpc":"2.0","id":1,"result":{` +
				`"content":[{"type":"text","text":"{\"gpus\":1}"}]}}`),
		},
		{
			NodeName: "node-2",
			PodName:  "pod-2",
			Response: []byte(`{"jsonrpc":"2.0","id":1,"result":{` +
				`"content":[{"type":"text","text":"{\"gpus\":2}"}]}}`),
		},
	}

	aggregated := handler.aggregateResults(context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "success", aggMap["status"])
	assert.Equal(t, 2, aggMap["success_count"])
	assert.Equal(t, 0, aggMap["error_count"])
	assert.Equal(t, 2, aggMap["node_count"])

	nodes := aggMap["nodes"].([]interface{})
	assert.Len(t, nodes, 2)
}

func TestAggregateResults_PartialSuccess(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "test",
		router:   &Router{},
	}

	results := []NodeResult{
		{
			NodeName: "node-1",
			PodName:  "pod-1",
			Response: []byte(`{"jsonrpc":"2.0","id":1,"result":{` +
				`"content":[{"type":"text","text":"{\"gpus\":1}"}]}}`),
		},
		{
			NodeName: "node-2",
			PodName:  "pod-2",
			Error:    "connection failed",
		},
	}

	aggregated := handler.aggregateResults(context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "partial", aggMap["status"])
	assert.Equal(t, 1, aggMap["success_count"])
	assert.Equal(t, 1, aggMap["error_count"])
}

func TestAggregateResults_AllError(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "test",
		router:   &Router{},
	}

	results := []NodeResult{
		{
			NodeName: "node-1",
			PodName:  "pod-1",
			Error:    "connection failed",
		},
		{
			NodeName: "node-2",
			PodName:  "pod-2",
			Error:    "timeout",
		},
	}

	aggregated := handler.aggregateResults(context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "error", aggMap["status"])
	assert.Equal(t, 0, aggMap["success_count"])
	assert.Equal(t, 2, aggMap["error_count"])
}

func TestAggregateResults_Empty(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "test",
		router:   &Router{},
	}

	results := []NodeResult{}

	aggregated := handler.aggregateResults(context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "success", aggMap["status"])
	assert.Equal(t, 0, aggMap["success_count"])
	assert.Equal(t, 0, aggMap["error_count"])
	assert.Equal(t, 0, aggMap["node_count"])
}

func TestAggregateGPUInventory_ClusterSummary(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "get_gpu_inventory",
		router:   &Router{},
	}

	// Mock response from two nodes with different GPU types
	node1Response := `{"jsonrpc":"2.0","id":0,"result":{}}` +
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text",` +
		`"text":"{\"driver_version\":\"575.57\",\"cuda_version\":\"12.9\",` +
		`\"device_count\":1,\"devices\":[{\"name\":\"Tesla T4\",` +
		`\"index\":0,\"uuid\":\"GPU-xxx\"}]}"}]}}`
	node2Response := `{"jsonrpc":"2.0","id":0,"result":{}}` +
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text",` +
		`"text":"{\"driver_version\":\"575.57\",\"cuda_version\":\"12.9\",` +
		`\"device_count\":2,\"devices\":[{\"name\":\"A100\",\"index\":0,` +
		`\"uuid\":\"GPU-yyy\"},{\"name\":\"A100\",\"index\":1,` +
		`\"uuid\":\"GPU-zzz\"}]}"}]}}`

	results := []NodeResult{
		{NodeName: "node1", PodName: "pod1", Response: []byte(node1Response)},
		{NodeName: "node2", PodName: "pod2", Response: []byte(node2Response)},
	}

	aggregated := handler.aggregateResults(context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "success", aggMap["status"])

	// Check cluster summary
	summary := aggMap["cluster_summary"].(map[string]interface{})
	assert.Equal(t, 2, summary["total_nodes"])
	assert.Equal(t, 2, summary["ready_nodes"])
	assert.Equal(t, 3, summary["total_gpus"])

	gpuTypes := summary["gpu_types"].([]string)
	assert.Len(t, gpuTypes, 2)
	assert.Contains(t, gpuTypes, "Tesla T4")
	assert.Contains(t, gpuTypes, "A100")

	// Check nodes array
	nodes := aggMap["nodes"].([]interface{})
	assert.Len(t, nodes, 2)

	node1Data := nodes[0].(map[string]interface{})
	assert.Equal(t, "node1", node1Data["name"])
	assert.Equal(t, "ready", node1Data["status"])
	assert.Equal(t, "575.57", node1Data["driver_version"])
}

func TestAggregateGPUInventory_WithErrors(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "get_gpu_inventory",
		router:   &Router{},
	}

	node1Response := `{"jsonrpc":"2.0","id":0,"result":{}}` +
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text",` +
		`"text":"{\"device_count\":1,\"devices\":[{\"name\":\"Tesla T4\",` +
		`\"index\":0}]}"}]}}`

	results := []NodeResult{
		{NodeName: "node1", PodName: "pod1", Response: []byte(node1Response)},
		{NodeName: "node2", PodName: "pod2", Error: "connection refused"},
	}

	aggregated := handler.aggregateResults(context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "success", aggMap["status"])

	summary := aggMap["cluster_summary"].(map[string]interface{})
	assert.Equal(t, 2, summary["total_nodes"])
	assert.Equal(t, 1, summary["ready_nodes"])
	assert.Equal(t, 1, summary["total_gpus"])

	nodes := aggMap["nodes"].([]interface{})
	node2Data := nodes[1].(map[string]interface{})
	assert.Equal(t, "error", node2Data["status"])
	assert.Equal(t, "connection refused", node2Data["error"])
}

func TestAggregateGPUInventory_Empty(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "get_gpu_inventory",
		router:   &Router{},
	}

	results := []NodeResult{}

	aggregated := handler.aggregateResults(context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "success", aggMap["status"])

	summary := aggMap["cluster_summary"].(map[string]interface{})
	assert.Equal(t, 0, summary["total_nodes"])
	assert.Equal(t, 0, summary["ready_nodes"])
	assert.Equal(t, 0, summary["total_gpus"])

	gpuTypes := summary["gpu_types"].([]string)
	assert.Len(t, gpuTypes, 0)
}

func TestFlattenGPUInfo(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]interface{}
		checkFn func(*testing.T, map[string]interface{})
	}{
		{
			name:  "nil input",
			input: nil,
			checkFn: func(t *testing.T, result map[string]interface{}) {
				assert.Contains(t, result, "error")
			},
		},
		{
			name: "basic fields",
			input: map[string]interface{}{
				"index": 0,
				"name":  "Tesla T4",
				"uuid":  "GPU-xxx",
			},
			checkFn: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, 0, result["index"])
				assert.Equal(t, "Tesla T4", result["name"])
				assert.Equal(t, "GPU-xxx", result["uuid"])
			},
		},
		{
			name: "with memory flattening",
			input: map[string]interface{}{
				"name": "Tesla T4",
				"memory": map[string]interface{}{
					"total_bytes": float64(16106127360),
				},
			},
			checkFn: func(t *testing.T, result map[string]interface{}) {
				memGB := result["memory_total_gb"].(float64)
				assert.InDelta(t, 15.0, memGB, 0.5)
			},
		},
		{
			name: "with temperature flattening",
			input: map[string]interface{}{
				"name": "Tesla T4",
				"temperature": map[string]interface{}{
					"current_celsius": float64(45),
				},
			},
			checkFn: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, 45, result["temperature_c"])
			},
		},
		{
			name: "with utilization flattening",
			input: map[string]interface{}{
				"name": "Tesla T4",
				"utilization": map[string]interface{}{
					"gpu_percent": float64(85),
				},
			},
			checkFn: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, 85, result["utilization_percent"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := flattenGPUInfo(tt.input)
			tt.checkFn(t, result)
		})
	}
}

func TestFilterGPULabels(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]string
		want    []string
		notWant []string
	}{
		{
			name: "filters to GPU-relevant labels",
			input: map[string]string{
				"nvidia.com/gpu.product":           "Tesla-T4",
				"nvidia.com/gpu.memory":            "16GB",
				"topology.kubernetes.io/zone":      "us-west-2a",
				"node.kubernetes.io/instance-type": "g4dn.xlarge",
				"kubernetes.io/arch":               "amd64",
				"kubernetes.io/os":                 "linux",
				"app.kubernetes.io/name":           "my-app",
				"random-label":                     "value",
			},
			want: []string{
				"nvidia.com/gpu.product",
				"nvidia.com/gpu.memory",
				"topology.kubernetes.io/zone",
				"node.kubernetes.io/instance-type",
				"kubernetes.io/arch",
				"kubernetes.io/os",
			},
			notWant: []string{
				"app.kubernetes.io/name",
				"random-label",
			},
		},
		{
			name: "includes exact match labels",
			input: map[string]string{
				"gpu-type":    "nvidia-a100",
				"accelerator": "gpu",
				"other":       "value",
			},
			want:    []string{"gpu-type", "accelerator"},
			notWant: []string{"other"},
		},
		{
			name:    "empty input",
			input:   map[string]string{},
			want:    []string{},
			notWant: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterGPULabels(tt.input)
			for _, k := range tt.want {
				assert.Contains(t, result, k)
			}
			for _, k := range tt.notWant {
				assert.NotContains(t, result, k)
			}
		})
	}
}

func TestGetNodeK8sMetadata(t *testing.T) {
	// Create fake K8s client with node
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				"nvidia.com/gpu.product":           "Tesla-T4",
				"topology.kubernetes.io/zone":      "us-west-2a",
				"node.kubernetes.io/instance-type": "g4dn.xlarge",
				"unrelated-label":                  "should-be-filtered",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodePIDPressure, Status: corev1.ConditionFalse},
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
			},
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(node)
	k8sClient := k8s.NewClientWithConfig(clientset, nil, "default")

	handler := &ProxyHandler{
		toolName: "get_gpu_inventory",
		router:   NewRouter(k8sClient),
	}

	ctx := context.Background()
	metadata, err := handler.getNodeK8sMetadata(ctx, "gpu-node-1")

	require.NoError(t, err)
	require.NotNil(t, metadata)

	// Check labels filtered correctly
	assert.Contains(t, metadata.Labels, "nvidia.com/gpu.product")
	assert.Contains(t, metadata.Labels, "topology.kubernetes.io/zone")
	assert.NotContains(t, metadata.Labels, "unrelated-label")

	// Check conditions
	assert.True(t, metadata.Conditions["Ready"])
	assert.False(t, metadata.Conditions["MemoryPressure"])
	assert.False(t, metadata.Conditions["DiskPressure"])

	// Check GPU resources
	require.NotNil(t, metadata.GPUResources)
	assert.Equal(t, int64(4), metadata.GPUResources.Capacity)
	assert.Equal(t, int64(4), metadata.GPUResources.Allocatable)
}

func TestGetNodeGPUAllocation(t *testing.T) {
	// Create fake client with pods requesting GPUs.
	// Note: This test verifies allocation counting within the client's
	// configured namespace. Cross-namespace counting is not supported
	// by the current implementation (see getNodeGPUAllocation docs).
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "training-job-1",
			Namespace: "ml-workloads",
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-1",
			Containers: []corev1.Container{
				{
					Name: "trainer",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "inference-job-1",
			Namespace: "ml-workloads",
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-1",
			Containers: []corev1.Container{
				{
					Name: "inference",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	// Completed pod should not count
	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "completed-job",
			Namespace: "ml-workloads",
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-1",
			Containers: []corev1.Container{
				{
					Name: "job",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(pod1, pod2, pod3)
	k8sClient := k8s.NewClientWithConfig(clientset, nil, "ml-workloads")

	handler := &ProxyHandler{
		toolName: "get_gpu_inventory",
		router:   NewRouter(k8sClient),
	}

	ctx := context.Background()
	allocated, err := handler.getNodeGPUAllocation(ctx, "gpu-node-1")

	require.NoError(t, err)
	// Should be 2 + 1 = 3 (completed pod excluded)
	assert.Equal(t, int64(3), allocated)
}

func TestAggregateGPUInventory_WithK8sMetadata(t *testing.T) {
	// Create fake K8s client with node and pods
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"nvidia.com/gpu.product":      "Tesla-T4",
				"topology.kubernetes.io/zone": "us-west-2a",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-workload",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node1",
			Containers: []corev1.Container{
				{
					Name: "worker",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(node, pod)
	k8sClient := k8s.NewClientWithConfig(clientset, nil, "default")

	handler := &ProxyHandler{
		toolName: "get_gpu_inventory",
		router:   NewRouter(k8sClient),
	}

	node1Response := `{"jsonrpc":"2.0","id":0,"result":{}}` +
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text",` +
		`"text":"{\"driver_version\":\"575.57\",\"cuda_version\":\"12.9\",` +
		`\"device_count\":4,\"devices\":[{\"name\":\"Tesla T4\",` +
		`\"index\":0,\"uuid\":\"GPU-xxx\"}]}"}]}}`

	results := []NodeResult{
		{NodeName: "node1", PodName: "pod1", Response: []byte(node1Response)},
	}

	// Test with K8s metadata enabled
	aggregated := handler.aggregateResults(
		context.Background(), results, true)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "success", aggMap["status"])

	// Check cluster summary has GPU resource counts
	summary := aggMap["cluster_summary"].(map[string]interface{})
	assert.Equal(t, int64(4), summary["gpus_capacity"])
	assert.Equal(t, int64(4), summary["gpus_allocatable"])
	assert.Equal(t, int64(2), summary["gpus_allocated"])
	assert.Equal(t, int64(2), summary["gpus_available"])

	// Check node has kubernetes metadata
	nodes := aggMap["nodes"].([]interface{})
	require.Len(t, nodes, 1)

	nodeData := nodes[0].(map[string]interface{})
	k8sMeta, ok := nodeData["kubernetes"].(*NodeK8sMetadata)
	require.True(t, ok, "kubernetes field should be *NodeK8sMetadata")

	assert.Contains(t, k8sMeta.Labels, "nvidia.com/gpu.product")
	assert.True(t, k8sMeta.Conditions["Ready"])
	assert.Equal(t, int64(4), k8sMeta.GPUResources.Capacity)
	assert.Equal(t, int64(2), k8sMeta.GPUResources.Allocated)
}

func TestAggregateGPUInventory_WithoutK8sMetadata(t *testing.T) {
	// Create fake K8s client with node
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"nvidia.com/gpu.product": "Tesla-T4",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
			},
		},
	}

	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset(node)
	k8sClient := k8s.NewClientWithConfig(clientset, nil, "default")

	handler := &ProxyHandler{
		toolName: "get_gpu_inventory",
		router:   NewRouter(k8sClient),
	}

	node1Response := `{"jsonrpc":"2.0","id":0,"result":{}}` +
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text",` +
		`"text":"{\"driver_version\":\"575.57\",\"cuda_version\":\"12.9\",` +
		`\"device_count\":4,\"devices\":[{\"name\":\"Tesla T4\",` +
		`\"index\":0,\"uuid\":\"GPU-xxx\"}]}"}]}}`

	results := []NodeResult{
		{NodeName: "node1", PodName: "pod1", Response: []byte(node1Response)},
	}

	// Test with K8s metadata disabled
	aggregated := handler.aggregateResults(
		context.Background(), results, false)
	aggMap := aggregated.(map[string]interface{})

	assert.Equal(t, "success", aggMap["status"])

	// Check cluster summary does NOT have GPU resource counts
	summary := aggMap["cluster_summary"].(map[string]interface{})
	assert.NotContains(t, summary, "gpus_capacity")
	assert.NotContains(t, summary, "gpus_allocatable")
	assert.NotContains(t, summary, "gpus_allocated")
	assert.NotContains(t, summary, "gpus_available")

	// Check node does NOT have kubernetes metadata
	nodes := aggMap["nodes"].([]interface{})
	require.Len(t, nodes, 1)

	nodeData := nodes[0].(map[string]interface{})
	assert.NotContains(t, nodeData, "kubernetes")
}

func TestProxyHandler_RateLimitExceeded(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "test_tool",
		router:   &Router{routingMode: RoutingModeHTTP},
	}
	// Set a very restrictive rate limit: 1 request, burst 1
	handler.limiter = rate.NewLimiter(1, 1)

	req := mcp.CallToolRequest{}
	req.Params.Name = "test_tool"

	// First request should succeed (but will fail later in routing — that's ok)
	// We just need to verify the limiter lets the first one through
	// and blocks subsequent ones.
	// Exhaust the burst
	_ = handler.limiter.Allow()

	// Second request should be rate limited
	result, err := handler.Handle(context.Background(), req)
	require.NoError(t, err)

	// Parse the result to check for rate limit error
	jsonBytes, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	assert.Contains(t, string(jsonBytes), "rate limit exceeded")
}

func TestProxyHandler_RateLimitDisabled(t *testing.T) {
	//nolint:staticcheck // NewSimpleClientset used for testing
	clientset := fake.NewSimpleClientset()
	k8sClient := k8s.NewClientWithConfig(clientset, nil, "default")

	handler := &ProxyHandler{
		toolName: "test_tool",
		router:   NewRouter(k8sClient),
		limiter:  nil, // disabled
	}

	// With nil limiter, should not panic and should proceed to routing
	// (it will fail in routing since there are no GPU nodes)
	req := mcp.CallToolRequest{}
	req.Params.Name = "test_tool"

	result, err := handler.Handle(context.Background(), req)
	require.NoError(t, err)
	// Should get a routing error, not a rate limit error
	jsonBytes, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(jsonBytes), "rate limit exceeded")
}

func TestWithRateLimit(t *testing.T) {
	handler := &ProxyHandler{
		toolName: "test_tool",
		router:   &Router{},
		limiter:  rate.NewLimiter(DefaultRateLimit, DefaultRateBurst),
	}

	opt := WithRateLimit(50, 10)
	opt(handler)

	assert.Equal(t, rate.Limit(50), handler.limiter.Limit())
	assert.Equal(t, 10, handler.limiter.Burst())
}
