# Quick Start Guide

Get started with `k8s-gpu-mcp-server` in under 5 minutes.

## Prerequisites

- **Go 1.25+** for building from source
- **Docker** or **Podman** for container builds
- **NVIDIA GPU** (optional, for real hardware testing)
- **Kubernetes cluster** (optional, for production deployment)

## Installation

### Option 1: Download Pre-built Binary

```bash
# Download latest release (coming in M4)
curl -LO https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/releases/latest/download/agent-linux-amd64
chmod +x agent-linux-amd64
mv agent-linux-amd64 /usr/local/bin/k8s-gpu-mcp-server
```

### Option 2: Build from Source

```bash
# Clone repository
git clone https://github.com/ArangoGutierrez/k8s-gpu-mcp-server.git
cd k8s-gpu-mcp-server

# Build agent
make agent

# Binary available at: bin/agent
./bin/agent --version
```

### Option 3: Pull Container Image

```bash
# Pull latest image (coming in M3)
docker pull ghcr.io/arangogutierrez/k8s-gpu-mcp-server:latest
```

## Basic Usage

### Local Testing (Mock Mode)

The agent includes mock NVML for testing without GPU hardware:

```bash
# Start agent in mock mode (default)
./bin/agent --mode=read-only --nvml-mode=mock

# In another terminal, send JSON-RPC requests
cat examples/initialize.json | ./bin/agent --nvml-mode=mock
cat examples/gpu_inventory.json | ./bin/agent --nvml-mode=mock
```

**Example Output:**
```json
{
  "status": "success",
  "device_count": 2,
  "devices": [
    {
      "Index": 0,
      "Name": "NVIDIA A100-SXM4-40GB (Mock 0)",
      "UUID": "GPU-00000000-0000-0000-0000-000000000000",
      "BusID": "0000:01:00.0",
      "MemoryTotal": 42949672960,
      "Temperature": 45,
      "PowerUsage": 150000,
      "GPUUtil": 30
    }
  ]
}
```

### Real GPU Testing

With NVIDIA GPU and driver installed:

```bash
# Start agent with real NVML
./bin/agent --mode=read-only --nvml-mode=real

# Test GPU inventory
cat examples/gpu_inventory.json | ./bin/agent --nvml-mode=real

# Or use single-line JSON-RPC
echo '{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}},"id":0}' | ./bin/agent --nvml-mode=real
echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get_gpu_inventory","arguments":{}},"id":1}' | ./bin/agent --nvml-mode=real
```

## Kubernetes Deployment

> 📖 For comprehensive Kubernetes deployment documentation, see the
> [Kubernetes Deployment Guide](kubernetes.md).

### Prerequisites

**GPU Access** — one of:
- **NVIDIA GPU Operator** (configures RuntimeClass automatically) — *recommended*
- **nvidia-ctk** configured with containerd/cri-o
- **NVIDIA Device Plugin** (fallback mode)

**Network Requirements** (for HTTP transport mode with gateway):
- Cross-node pod-to-pod connectivity must work
- If using **Calico CNI on AWS** with nodes in the same subnet, ensure VXLAN
  encapsulation is enabled:

```bash
# Check current mode
kubectl get ippool default-ipv4-ippool -o jsonpath='{.spec.vxlanMode}'

# If shows "CrossSubnet" and cross-node HTTP fails, change to "Always":
kubectl patch installation default --type=json \
  -p='[{"op": "replace", "path": "/spec/calicoNetwork/ipPools/0/encapsulation", "value": "VXLAN"}]'
```

> 📖 See [Cross-Node Networking Troubleshooting](troubleshooting/cross-node-networking.md)
> for detailed diagnosis and fix options.

### Install with Helm

```bash
# Add the chart (if published) or use local path
helm install k8s-gpu-mcp-server ./deployment/helm/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics \
  --create-namespace

# Verify pods are running on GPU nodes
kubectl get pods -n gpu-diagnostics -o wide

# Verify HTTP endpoints are working
kubectl port-forward -n gpu-diagnostics svc/k8s-gpu-mcp-server-gateway 8080:8080 &
curl -s http://localhost:8080/healthz  # Should return {"status":"healthy"}
curl -s http://localhost:8080/readyz   # Should return {"status":"ready"}
```

> **Note:** The default deployment uses HTTP transport mode. Agents run as
> persistent HTTP servers and the gateway routes requests via HTTP for
> low-latency, low-memory operation.

### Fallback Mode (No RuntimeClass)

For clusters without RuntimeClass configured (e.g., cri-dockerd):

```bash
helm install k8s-gpu-mcp-server ./deployment/helm/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics \
  --create-namespace \
  --set gpu.runtimeClass.enabled=false \
  --set gpu.resourceRequest.enabled=true
```

> ⚠️ **Warning:** Fallback mode requests `nvidia.com/gpu` resources, consuming
> GPU capacity from the scheduler.

### Start Diagnostic Session

```bash
# Find agent pod on target node
NODE_NAME=gpu-node-5
POD=$(kubectl get pods -n gpu-diagnostics \
  -l app.kubernetes.io/name=k8s-gpu-mcp-server \
  --field-selector spec.nodeName=$NODE_NAME \
  -o jsonpath='{.items[0].metadata.name}')

# Start read-only diagnostic session
kubectl exec -it -n gpu-diagnostics $POD -- /agent --mode=read-only

# With operator mode (enables kill/reset operations)
kubectl exec -it -n gpu-diagnostics $POD -- /agent --mode=operator
```

### GPU Access Modes

| Mode | Helm Values | Description |
|------|-------------|-------------|
| **RuntimeClass** (default) | `gpu.runtimeClass.enabled=true` | Uses nvidia RuntimeClass + `NVIDIA_VISIBLE_DEVICES=all` |
| **Resource Request** | `gpu.resourceRequest.enabled=true` | Requests `nvidia.com/gpu` from device plugin |

The default RuntimeClass mode does NOT request `nvidia.com/gpu` resources,
allowing the agent to monitor all GPUs without blocking the scheduler.

## Available Tools

Once the agent is running, you can call these MCP tools:

| Tool | Category | Description |
|------|----------|-------------|
| `get_gpu_inventory` | NVML | Hardware inventory + telemetry |
| `get_gpu_health` | NVML | Health monitoring with scoring |
| `analyze_xid_errors` | NVML | XID error parsing from kernel logs |
| `describe_gpu_node` | K8s + NVML | Node-level diagnostics |
| `get_pod_gpu_allocation` | K8s | GPU-to-Pod correlation |

### 1. GPU Inventory
**Purpose:** Get hardware inventory and current telemetry

```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "get_gpu_inventory",
    "arguments": {}
  },
  "id": 2
}
```

**Response includes:**
- GPU model name
- UUID (unique identifier)
- PCI Bus ID
- Memory (total, used, free)
- Temperature (°C)
- Power usage (milliwatts)
- Utilization (GPU %, Memory %)

### 2. GPU Health
**Purpose:** Health monitoring with scoring and recommendations

```bash
echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get_gpu_health","arguments":{}},"id":3}' | ./bin/agent
```

### 3. XID Error Analysis
**Purpose:** Parse NVIDIA XID errors from kernel logs

```bash
echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"analyze_xid_errors","arguments":{}},"id":4}' | ./bin/agent
```

### 4. Describe GPU Node (Kubernetes)
**Purpose:** Comprehensive node view with K8s metadata

```bash
echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"describe_gpu_node","arguments":{"node_name":"gpu-node-1"}},"id":5}' | ./bin/agent
```

### 5. Pod GPU Allocation (Kubernetes)
**Purpose:** GPU-to-Pod correlation

```bash
echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get_pod_gpu_allocation","arguments":{"node_name":"gpu-node-1"}},"id":6}' | ./bin/agent
```

📖 **[Full MCP Usage Guide →](mcp-usage.md)**

## Common Workflows

### Debugging a Stuck Training Job

```bash
# 1. Connect to the problematic node
kubectl debug node/gpu-node-5 \
  --image=ghcr.io/arangogutierrez/k8s-gpu-mcp-server:latest \
  --profile=sysadmin \
  -- /bin/bash

# 2. Inside the debug pod, run agent
/agent --mode=read-only --nvml-mode=real

# 3. Query GPU inventory (from Claude Desktop or another terminal)
# Send JSON-RPC requests via stdin
```

### Checking GPU Health

```bash
# Get inventory to check temperatures and power
cat examples/gpu_inventory.json | ./bin/agent --nvml-mode=real | jq '.devices[].Temperature'

# Look for high temps (>80°C) or power throttling
```

## Flags Reference

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `read-only` | Operation mode: `read-only` or `operator` |
| `--nvml-mode` | `mock` | NVML mode: `mock` or `real` |
| `--port` | `0` | HTTP port (0 = stdio mode, >0 = HTTP mode) |
| `--addr` | `0.0.0.0` | HTTP listen address |
| `--gateway` | `false` | Enable gateway mode (routes to node agents) |
| `--routing-mode` | `http` | Gateway routing: `http` (direct) or `exec` (legacy) |
| `--namespace` | `gpu-diagnostics` | Namespace for GPU agent pods (gateway mode) |
| `--oneshot` | `0` | Exit after N requests (0=disabled) |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--version` | - | Show version and exit |

## Troubleshooting

### "Failed to initialize NVML"

**Cause:** NVIDIA driver not found or GPU not available

**Solution:**
```bash
# Check NVIDIA driver
nvidia-smi

# If not found, install NVIDIA driver
# Ubuntu: sudo apt-get install nvidia-driver-XXX

# Or use mock mode for testing
./bin/agent --nvml-mode=mock
```

### "Real NVML requires CGO"

**Cause:** Binary built without CGO support

**Solution:**
```bash
# Rebuild with CGO enabled
CGO_ENABLED=1 go build -o bin/agent ./cmd/agent

# Or use pre-built binaries with CGO
```

### "Parse error" in JSON-RPC

**Cause:** MCP protocol requires initialization handshake first

**Solution:**
```bash
# Always send initialize first
echo '{"jsonrpc":"2.0","method":"initialize",...}' | ./bin/agent
# Then send tool calls
echo '{"jsonrpc":"2.0","method":"tools/call",...}' | ./bin/agent
```

### Gateway returns "all nodes failed" (HTTP mode)

**Cause:** Cross-node pod networking issue (common on AWS with Calico CNI)

**Symptoms:**
- Gateway health check passes (`/healthz` returns healthy)
- Same-node agent works, cross-node agents timeout
- Gateway logs show HTTP request timeouts

**Solution:**
```bash
# Test cross-node connectivity from gateway pod
GATEWAY_POD=$(kubectl get pods -n gpu-diagnostics -l app.kubernetes.io/component=gateway -o jsonpath='{.items[0].metadata.name}')
AGENT_IP=$(kubectl get pods -n gpu-diagnostics -l app.kubernetes.io/component=gpu-diagnostics -o jsonpath='{.items[0].status.podIP}')
kubectl exec -n gpu-diagnostics $GATEWAY_POD -- wget -q -O - --timeout=5 http://$AGENT_IP:8080/healthz

# If timeout, check Calico VXLAN mode (AWS specific)
kubectl get ippool default-ipv4-ippool -o jsonpath='{.spec.vxlanMode}'
# If "CrossSubnet", change to "Always" - see Prerequisites section
```

> 📖 Full guide: [Cross-Node Networking Troubleshooting](troubleshooting/cross-node-networking.md)

## Next Steps

- Read the [Architecture Documentation](architecture.md) to understand the design
- See [MCP Usage Guide](mcp-usage.md) for detailed protocol examples
- Check [DEVELOPMENT.md](../DEVELOPMENT.md) for contributing
- View [Examples](../examples/) for more JSON-RPC requests

## Getting Help

- 📖 [Full Documentation](README.md)
- 🐛 [Report Issues](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/issues)
- 💬 [Discussions](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/discussions)
- 📧 Contact: [@ArangoGutierrez](https://github.com/ArangoGutierrez)

