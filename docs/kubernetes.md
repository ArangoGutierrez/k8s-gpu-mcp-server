# Kubernetes Deployment Guide

This guide covers deploying and operating `k8s-gpu-mcp-server` in Kubernetes clusters.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Architecture Overview](#architecture-overview)
- [Connecting to the Gateway](#connecting-to-the-gateway)
- [AI Assistant Integration](#ai-assistant-integration)
- [Configuration Reference](#configuration-reference)
- [Troubleshooting](#troubleshooting)

## Prerequisites

### Kubernetes Requirements

| Requirement | Minimum | Recommended |
|-------------|---------|-------------|
| Kubernetes | 1.28+ | 1.30+ |
| Helm | 3.x | 3.14+ |

### GPU Access

The agent requires GPU access via one of these methods:

| Method | Description | Recommended |
|--------|-------------|-------------|
| **NVIDIA GPU Operator** | Full-stack GPU management, configures RuntimeClass automatically | ✅ Yes |
| **nvidia-ctk** | Manual containerd/cri-o configuration | Yes |
| **NVIDIA Device Plugin** | Fallback mode, consumes GPU resources | No |

**Verify GPU Operator is working:**

```bash
# Check RuntimeClass exists
kubectl get runtimeclass nvidia

# Check GPU nodes are labeled
kubectl get nodes -l nvidia.com/gpu.present=true
```

### Network Requirements (HTTP Transport)

Cross-node pod-to-pod connectivity must work for HTTP transport mode:

```bash
# Check Calico VXLAN mode (if using Calico CNI on AWS)
kubectl get ippool default-ipv4-ippool -o jsonpath='{.spec.vxlanMode}'

# If shows "CrossSubnet" and cross-node HTTP fails, change to "Always":
kubectl patch installation default --type=json \
  -p='[{"op": "replace", "path": "/spec/calicoNetwork/ipPools/0/encapsulation", "value": "VXLAN"}]'
```

> 📖 See [Cross-Node Networking Troubleshooting](troubleshooting/cross-node-networking.md) for detailed diagnosis.

## Installation

### Option 1: Helm OCI Registry (Recommended)

```bash
# Install from GHCR OCI registry
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics \
  --create-namespace

# Verify deployment
kubectl get pods -n gpu-diagnostics -o wide
kubectl get daemonset -n gpu-diagnostics
```

### Option 2: Local Chart

```bash
# Clone repository
git clone https://github.com/ArangoGutierrez/k8s-gpu-mcp-server.git
cd k8s-gpu-mcp-server

# Install from local chart
helm install k8s-gpu-mcp-server ./deployment/helm/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics \
  --create-namespace
```

### Option 3: With Gateway (Multi-Node Clusters)

For clusters with multiple GPU nodes, deploy with the gateway for unified access:

```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics \
  --create-namespace \
  --set gateway.enabled=true
```

### Fallback Mode (No RuntimeClass)

For clusters without RuntimeClass configured (e.g., cri-dockerd):

```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics \
  --create-namespace \
  --set gpu.runtimeClass.enabled=false \
  --set gpu.resourceRequest.enabled=true
```

> ⚠️ **Warning:** Fallback mode requests `nvidia.com/gpu` resources, consuming GPU capacity from the scheduler.

### Verify Installation

```bash
# Check pods are running on GPU nodes
kubectl get pods -n gpu-diagnostics -o wide

# Check agent logs
kubectl logs -n gpu-diagnostics -l app.kubernetes.io/component=gpu-diagnostics --tail=20

# Test health endpoint (HTTP mode)
kubectl port-forward -n gpu-diagnostics svc/k8s-gpu-mcp-server 8080:8080 &
curl -s http://localhost:8080/healthz
# Expected: {"status":"healthy"}
```

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                    MCP Client (Claude/Cursor)                        │
└────────────────────────────┬────────────────────────────────────────┘
                             │ stdio / HTTP
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Gateway Pod (:8080)                               │
│       Router → Circuit Breaker → HTTP Client                         │
└────────────────────────────┬────────────────────────────────────────┘
                             │ HTTP (pod-to-pod)
         ┌───────────────────┼───────────────────┐
         ▼                   ▼                   ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│  Agent (Node 1) │  │  Agent (Node 2) │  │  Agent (Node N) │
│  DaemonSet Pod  │  │  DaemonSet Pod  │  │  DaemonSet Pod  │
│  NVML → GPU     │  │  NVML → GPU     │  │  NVML → GPU     │
└─────────────────┘  └─────────────────┘  └─────────────────┘
```

### Components

| Component | Type | Description |
|-----------|------|-------------|
| **Agent** | DaemonSet | Runs on each GPU node, provides MCP tools via HTTP |
| **Gateway** | Deployment | Single entry point, routes to all agents (optional) |

### Transport Modes

| Mode | Default | Latency | Use Case |
|------|---------|---------|----------|
| **HTTP** | ✅ Yes | ~12-57ms | Production, gateway routing |
| **Stdio** | No | ~30s | Direct debugging via kubectl exec |

### Gateway Routing Modes

When the gateway is enabled, it routes MCP requests to agent pods using one of
two modes:

| Mode | Flag | Description | Performance |
|------|------|-------------|-------------|
| **HTTP** (default) | `--routing-mode=http` | Direct HTTP to agent pods via pod IP | ~12-57ms latency |
| **Exec** (legacy) | `--routing-mode=exec` | Routes via Kubernetes API server exec | ~30s latency |

**HTTP routing** (default) sends requests directly to agent pod IPs over the
cluster network. This requires functional cross-node pod-to-pod networking
(see [Network Requirements](#network-requirements-http-transport)).

**Exec routing** is a legacy fallback that routes through the Kubernetes API
server. Use this when cross-node networking is not available:

```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set gateway.enabled=true \
  --set gateway.routingMode=exec
```

> 📖 See [Architecture Documentation](architecture.md) for detailed design.

## Connecting to the Gateway

### Port-Forward (Development)

```bash
# Forward gateway service to localhost
kubectl port-forward -n gpu-diagnostics svc/k8s-gpu-mcp-server-gateway 8080:8080
```

### Test with curl

```bash
# List available tools
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'

# Get GPU inventory from all nodes
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {"name": "get_gpu_inventory"}
  }'
```

### Health Endpoints

| Endpoint | Description |
|----------|-------------|
| `/healthz` | Liveness probe - agent is running |
| `/readyz` | Readiness probe - agent can serve requests |
| `/metrics` | Prometheus metrics |

```bash
# Check all endpoints
curl -s http://localhost:8080/healthz  # {"status":"healthy"}
curl -s http://localhost:8080/readyz   # {"status":"ready"}
curl -s http://localhost:8080/metrics  # Prometheus format
```

## AI Assistant Integration

### Claude Desktop (via Gateway)

For deployed clusters with gateway enabled:

**macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "k8s-gpu-mcp": {
      "command": "npx",
      "args": ["-y", "k8s-gpu-mcp-server@latest", "--gateway-url", "http://localhost:8080"]
    }
  }
}
```

> **Note:** Requires `kubectl port-forward` running to forward gateway service.

### Claude Desktop (via kubectl exec)

For direct access without gateway:

```json
{
  "mcpServers": {
    "k8s-gpu-agent": {
      "command": "kubectl",
      "args": ["exec", "-i", "deploy/k8s-gpu-mcp-server", "-n", "gpu-diagnostics", "--", "/agent"]
    }
  }
}
```

### Cursor / VS Code

Add to `~/.cursor/mcp.json` (Cursor) or VS Code MCP config:

**Via Gateway:**
```json
{
  "mcpServers": {
    "k8s-gpu-mcp": {
      "command": "npx",
      "args": ["-y", "k8s-gpu-mcp-server@latest", "--gateway-url", "http://localhost:8080"]
    }
  }
}
```

**Via kubectl exec:**
```json
{
  "mcpServers": {
    "k8s-gpu-agent": {
      "command": "kubectl",
      "args": ["exec", "-i", "deploy/k8s-gpu-mcp-server", "-n", "gpu-diagnostics", "--", "/agent"]
    }
  }
}
```

### Direct kubectl Session

For interactive debugging on a specific node:

```bash
# Find agent pod on target node
NODE_NAME=gpu-node-5
POD=$(kubectl get pods -n gpu-diagnostics \
  -l app.kubernetes.io/name=k8s-gpu-mcp-server \
  --field-selector spec.nodeName=$NODE_NAME \
  -o jsonpath='{.items[0].metadata.name}')

# Start interactive session
kubectl exec -it -n gpu-diagnostics $POD -- /agent --mode=read-only
```

## Configuration Reference

### Key Helm Values

| Value | Default | Description |
|-------|---------|-------------|
| `agent.mode` | `read-only` | Operation mode: `read-only` or `operator` |
| `agent.nvmlMode` | `real` | NVML mode: `real` or `mock` |
| `gpu.runtimeClass.enabled` | `true` | Use nvidia RuntimeClass for GPU access |
| `gpu.runtimeClass.name` | `nvidia` | RuntimeClass name |
| `gpu.resourceRequest.enabled` | `false` | Request nvidia.com/gpu resources (fallback) |
| `transport.mode` | `http` | Transport: `http` or `stdio` |
| `transport.http.port` | `8080` | HTTP listen port |
| `gateway.enabled` | `false` | Deploy gateway for multi-node access |
| `gateway.replicas` | `2` | Gateway replicas (HA) |
| `gateway.routingMode` | `http` | Routing: `http` or `exec` |
| `networkPolicy.enabled` | `false` | Enable NetworkPolicy |

### Common Configurations

**Development (Mock GPUs):**
```bash
helm install k8s-gpu-mcp-server ./deployment/helm/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set agent.nvmlMode=mock
```

**Production with Gateway:**
```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set gateway.enabled=true \
  --set gateway.replicas=2 \
  --set networkPolicy.enabled=true
```

**Single-Node Debugging:**
```bash
helm install k8s-gpu-mcp-server ./deployment/helm/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set gateway.enabled=false
```

### Port Configuration

The agent HTTP server and related endpoints use the following ports:

| Port | Service | Description |
|------|---------|-------------|
| `8080` | MCP HTTP | Main MCP JSON-RPC endpoint (`/mcp`) and health probes |
| `8080` | Health | Liveness (`/healthz`), readiness (`/readyz`) |
| `8080` | Metrics | Prometheus metrics (`/metrics`) |

To change the HTTP port:

```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set transport.http.port=9090
```

When using `kubectl port-forward`, map to the configured port:

```bash
# Default port (8080)
kubectl port-forward -n gpu-diagnostics svc/k8s-gpu-mcp-server 8080:8080

# Custom port (9090)
kubectl port-forward -n gpu-diagnostics svc/k8s-gpu-mcp-server 9090:9090
```

### DCGM Integration (Advanced)

For datacenter GPUs (Tesla, A100, H100) with DCGM support, the agent can
optionally integrate with NVIDIA DCGM for advanced telemetry including
profiling metrics, NVSwitch monitoring, and native XID error collection.

**DCGM Helm Values:**

| Value | Default | Description |
|-------|---------|-------------|
| `agent.dcgm.enabled` | `false` | Enable DCGM integration |
| `agent.dcgm.mode` | `embedded` | DCGM mode: `embedded` or `external` |
| `agent.dcgm.socket` | `/var/run/dcgm.sock` | Socket path for external mode |

**Embedded mode** (self-contained, starts nv-hostengine internally):

```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set agent.dcgm.enabled=true \
  --set agent.dcgm.mode=embedded
```

**External mode** (connects to existing DCGM daemon):

```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set agent.dcgm.enabled=true \
  --set agent.dcgm.mode=external \
  --set agent.dcgm.socket=/var/run/dcgm.sock
```

DCGM is optional. When unavailable, the agent falls back to NVML-only mode
with no loss of core functionality. See [Architecture - DCGM Integration](architecture.md#dcgm-integration-pkgdcgm) for details.

> 📖 See [Helm Chart README](../deployment/helm/k8s-gpu-mcp-server/README.md) for full values reference.

## Troubleshooting

### Agent Pod Not Scheduling

**Symptom:** Agent pods are Pending on GPU nodes.

**Check:**
```bash
kubectl describe pod -n gpu-diagnostics -l app.kubernetes.io/component=gpu-diagnostics
```

**Solutions:**

| Cause | Solution |
|-------|----------|
| RuntimeClass not found | Install GPU Operator or configure nvidia-ctk |
| GPU taint not tolerated | Verify tolerations in values.yaml |
| Node selector mismatch | Check `nodeSelector` matches GPU node labels |

### "Failed to initialize NVML"

**Cause:** GPU driver not accessible in container.

**Solutions:**
```bash
# Verify RuntimeClass exists
kubectl get runtimeclass nvidia

# If missing, ensure GPU Operator is deployed
kubectl get pods -n gpu-operator

# Or use fallback mode
helm upgrade k8s-gpu-mcp-server ... \
  --set gpu.runtimeClass.enabled=false \
  --set gpu.resourceRequest.enabled=true
```

### Gateway Returns "all nodes failed"

**Cause:** Cross-node pod networking issue (common on AWS with Calico CNI).

**Diagnosis:**
```bash
# Test from gateway pod
GATEWAY_POD=$(kubectl get pods -n gpu-diagnostics \
  -l app.kubernetes.io/component=gateway \
  -o jsonpath='{.items[0].metadata.name}')

AGENT_IP=$(kubectl get pods -n gpu-diagnostics \
  -l app.kubernetes.io/component=gpu-diagnostics \
  -o jsonpath='{.items[0].status.podIP}')

kubectl exec -n gpu-diagnostics $GATEWAY_POD -- \
  wget -q -O - --timeout=5 http://$AGENT_IP:8080/healthz
```

**Solutions:**
1. Enable VXLAN Always mode (see [Prerequisites](#network-requirements-http-transport))
2. Use exec routing: `--set gateway.routingMode=exec`

> 📖 Full guide: [Cross-Node Networking Troubleshooting](troubleshooting/cross-node-networking.md)

### RBAC Permission Errors

**Symptom:** Tools return "forbidden" errors.

**Solutions:**
```bash
# Verify RBAC is created
kubectl get clusterrole | grep k8s-gpu-mcp

# Check agent permissions
kubectl auth can-i get nodes \
  --as=system:serviceaccount:gpu-diagnostics:k8s-gpu-mcp-server

# If missing, ensure RBAC is enabled
helm upgrade k8s-gpu-mcp-server ... --set agent.rbac.create=true
```

> 📖 See [Security Model](security.md) for detailed RBAC configuration.

## Related Documentation

- [Quick Start Guide](quickstart.md) - Get running in 5 minutes
- [Architecture](architecture.md) - System design and components
- [Security Model](security.md) - RBAC and security configuration
- [MCP Usage](mcp-usage.md) - How to use MCP tools
