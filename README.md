# k8s-gpu-mcp-server

**Just-in-Time SRE Diagnostic Agent for NVIDIA GPU Clusters on Kubernetes**

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![CI Status](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/workflows/CI/badge.svg)](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/actions)
[![GitHub Issues](https://img.shields.io/github/issues/ArangoGutierrez/k8s-gpu-mcp-server)](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/issues)
[![GitHub Stars](https://img.shields.io/github/stars/ArangoGutierrez/k8s-gpu-mcp-server?style=social)](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server)
[![MCP](https://img.shields.io/badge/MCP-2025--06--18-purple)](https://modelcontextprotocol.io/)

---

## Overview

`k8s-gpu-mcp-server` is an **ephemeral diagnostic agent** that provides surgical, 
real-time NVIDIA GPU hardware introspection for Kubernetes clusters via the 
[Model Context Protocol (MCP)](https://modelcontextprotocol.io/). 

Unlike traditional monitoring systems, this agent is designed for **AI-assisted 
troubleshooting** by SREs debugging complex hardware failures that standard 
Kubernetes APIs cannot detect.

### ✨ Key Features

- 🎯 **Low-Footprint, Always-Available** - Persistent HTTP server (~15-20MB idle) performs GPU work only when tools are invoked
- 🔌 **HTTP Transport** - JSON-RPC 2.0 over HTTP/SSE (production default)
- 🔍 **Deep Hardware Access** - Direct NVML integration for GPU diagnostics
- 🤖 **AI-Native** - Built for Claude Desktop, Cursor, and MCP-compatible hosts
- 📋 **MCP Prompts** - Pre-built GPU diagnostic workflows for guided troubleshooting
- 🔒 **Secure by Default** - Read-only operations with explicit operator mode
- ⚡ **Production Ready** - Real Tesla T4 testing, 550+ tests passing

---

## 🚀 Quick Start

### One-Click Install

[![Install MCP Server](https://cursor.com/deeplink/mcp-install-dark.svg)](https://cursor.com/install-mcp?name=k8s-gpu-mcp&config=eyJtY3BTZXJ2ZXJzIjp7Ims4cy1ncHUtbWNwIjp7ImNvbW1hbmQiOiJucHgiLCJhcmdzIjpbIi15IiwiazhzLWdwdS1tY3Atc2VydmVyQGxhdGVzdCJdfX19Cg==)

Click the button above to install automatically in Cursor.

### One-Line Installation

```bash
# Using npx (recommended)
npx k8s-gpu-mcp-server@latest

# Or install globally
npm install -g k8s-gpu-mcp-server
```

<details>
<summary><strong>📋 Manual Configuration: Cursor / VS Code</strong></summary>

Add to `~/.cursor/mcp.json` (Cursor) or VS Code MCP config:

```json
{
  "mcpServers": {
    "k8s-gpu-mcp": {
      "command": "npx",
      "args": ["-y", "k8s-gpu-mcp-server@latest"]
    }
  }
}
```

</details>

<details>
<summary><strong>📋 Manual Configuration: Claude Desktop</strong></summary>

**macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`  
**Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "k8s-gpu-mcp": {
      "command": "npx",
      "args": ["-y", "k8s-gpu-mcp-server@latest"]
    }
  }
}
```

</details>

### Install from Source

```bash
# Clone and build
git clone https://github.com/ArangoGutierrez/k8s-gpu-mcp-server.git
cd k8s-gpu-mcp-server
make agent

# Test with mock GPUs (no hardware required)
cat examples/gpu_inventory.json | ./bin/agent --nvml-mode=mock

# Test with real GPU (requires NVIDIA driver)
cat examples/gpu_inventory.json | ./bin/agent --nvml-mode=real
```

### Deploy to Kubernetes

```bash
# Deploy with Helm OCI (recommended)
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace

# Or from local chart
helm install k8s-gpu-mcp-server ./deployment/helm/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace

# Find agent pod on target node
NODE_NAME=<node-name>
POD=$(kubectl get pods -n gpu-diagnostics \
  -l app.kubernetes.io/name=k8s-gpu-mcp-server \
  --field-selector spec.nodeName=$NODE_NAME \
  -o jsonpath='{.items[0].metadata.name}')

# Start diagnostic session
kubectl exec -it -n gpu-diagnostics $POD -- /agent --mode=read-only
```

> **Note:** GPU access requires `runtimeClassName: nvidia` configured by
> GPU Operator or nvidia-ctk. For clusters without RuntimeClass, use fallback:
> `--set gpu.runtimeClass.enabled=false --set gpu.resourceRequest.enabled=true`

### Configure Claude Desktop with kubectl (Advanced)

For deployed agents, add to your Claude Desktop configuration:

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

Then ask Claude: *"What's the temperature of the GPUs?"*

📖 **[Full Quick Start Guide →](docs/quickstart.md)** | **[Kubernetes Deployment →](docs/kubernetes.md)**

---

## 📊 Architecture

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
│  5 MCP Tools    │  │  5 MCP Tools    │  │  5 MCP Tools    │
│  NVML → GPU     │  │  NVML → GPU     │  │  NVML → GPU     │
└─────────────────┘  └─────────────────┘  └─────────────────┘
```

**Design Principles:**
- **HTTP-First**: Gateway routes via HTTP to agent pods (~50ms latency)
- **Low Footprint**: Persistent HTTP server, ~15-20MB memory
- **Observable**: Circuit breaker, Prometheus metrics, distributed tracing
- **Interface Abstraction**: Testable, flexible, portable (538 tests)

📖 **[Architecture Documentation →](docs/architecture.md)**

---

## 🛠️ Available Tools

| Tool | Description | Status |
|------|-------------|--------|
| `get_gpu_inventory` | Hardware inventory + telemetry | ✅ Available |
| `get_gpu_health` | GPU health monitoring with scoring | ✅ Available |
| `analyze_xid_errors` | Parse GPU XID error codes from kernel logs | ✅ Available |
| `describe_gpu_node` | Node-level GPU diagnostics with K8s metadata | ✅ Available |
| `get_pod_gpu_allocation` | GPU-to-Pod correlation via resource requests | ✅ Available |
| `kill_gpu_process` | Terminate GPU process | 🚧 M4 (Operator) |
| `reset_gpu` | GPU reset | 🚧 M4 (Operator) |

### 📋 Available Prompts

MCP Prompts provide guided diagnostic workflows that orchestrate multiple tools:

| Prompt | Description |
|--------|-------------|
| `gpu-health-check` | Comprehensive GPU health assessment with recommendations |
| `diagnose-xid-errors` | Analyze NVIDIA XID errors with remediation guidance |
| `gpu-triage` | Standard SRE triage workflow: inventory → health → XID analysis |

**Example usage with Claude:**
```
You: "Run the GPU triage workflow for node gpu-worker-5"

Claude: [Executes gpu-triage prompt]
        → Calls get_gpu_inventory, get_gpu_health, analyze_xid_errors
        → Returns structured triage report with recommendations
```

📖 **[MCP Usage Guide →](docs/mcp-usage.md)**

---

## 📈 Project Status

### Current Milestone: [M3: Kubernetes Integration](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/milestone/3)
**Progress:** ~90% Complete (HTTP Transport ✅, Gateway ✅, K8s Tools ✅)

### Completed Milestones
- ✅ [M1: Foundation & API](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/milestone/1) - Completed Jan 3, 2026
- ✅ [M2: Hardware Introspection](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/milestone/2) - Completed Jan 10, 2026
  - Real NVML integration, tested on Tesla T4
  - GPU health monitoring, XID error analysis
  - npm/Helm distribution

### Recent Updates (Jan 2026)
- **Jan 17**: MCP Prompts support - 3 built-in GPU diagnostic workflows
- **Jan 16**: Documentation 360 review for external contributors
- **Jan 15**: K8s tools complete (`describe_gpu_node`, `get_pod_gpu_allocation`)
- **Jan 14**: HTTP Transport Epic complete - 150× latency improvement
- **Jan 14**: Cross-node networking fix (Calico VXLAN)
- **Jan 13**: Gateway mode with circuit breaker & Prometheus metrics

📊 **[View All Milestones →](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/milestones)**

---

## 🧪 Testing

### Unit Tests (No GPU Required)

```bash
make test                   # Run all unit tests (538 tests passing)
make coverage               # Generate coverage report
make coverage-html          # View coverage in browser
```

### Integration Tests (Requires GPU)

```bash
make test-integration       # Run on GPU hardware
# Or manually:
go test -tags=integration -v ./pkg/nvml/
```

**Latest Test Results:**
```
✓ 538 total tests passing
✓ Race detector enabled (-race)
✓ Coverage: 58-80% by package

Integration tested on Tesla T4:
  - GPU: Tesla T4 (15GB)
  - Temperature: 29°C
  - Power: 13.9W
  - All NVML operations verified
```

---

## 🏗️ Build

```bash
# Build for local platform
make agent

# Build for Linux (with real NVML)
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 make agent

# Build container image
make image

# Multi-arch release builds
make dist
```

**Binary Sizes:**
- Mock mode: **4.3MB** (CGO disabled)
- Real mode: **7.9MB** (CGO enabled)

---

## 📦 Installation

### Using npm (Recommended)

```bash
# Run directly with npx
npx k8s-gpu-mcp-server@latest

# Or install globally
npm install -g k8s-gpu-mcp-server
```

### From Source

```bash
git clone https://github.com/ArangoGutierrez/k8s-gpu-mcp-server.git
cd k8s-gpu-mcp-server
make agent
sudo mv bin/agent /usr/local/bin/k8s-gpu-mcp-server
```

### Using Go

```bash
go install github.com/ArangoGutierrez/k8s-gpu-mcp-server/cmd/agent@latest
```

### Container Image

```bash
docker pull ghcr.io/arangogutierrez/k8s-gpu-mcp-server:latest
```

### Helm Chart (OCI)

```bash
# Install from GHCR OCI registry
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace
```

---

## 🤝 Contributing

We welcome contributions! Please see our [Development Guide](DEVELOPMENT.md)
for details.

### Quick Contribution Guide

1. Check [open issues](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/issues)
2. Fork and create feature branch: `git checkout -b feat/my-feature`
3. Make changes, add tests
4. Run checks: `make all`
5. Commit with DCO: `git commit -s -S -m "feat(scope): description"`
6. Open PR with labels and milestone

📖 **[Full Development Guide →](DEVELOPMENT.md)**

---

## 📚 Documentation

- **[Quick Start Guide](docs/quickstart.md)** - Get running in 5 minutes
- **[Kubernetes Deployment](docs/kubernetes.md)** - K8s deployment and configuration
- **[Architecture](docs/architecture.md)** - System design and components
- **[Security Model](docs/security.md)** - RBAC and security configuration
- **[MCP Usage](docs/mcp-usage.md)** - How to consume the MCP server
- **[Development Guide](DEVELOPMENT.md)** - Contributing guidelines
- **[Examples](examples/)** - Sample JSON-RPC requests

---

## 🔧 Technology Stack

- **Language**: Go 1.25+ (latest stable)
- **MCP Protocol**: [mcp-go v0.43.2](https://github.com/mark3labs/mcp-go)
- **GPU Library**: [go-nvml v0.13.0-1](https://github.com/NVIDIA/go-nvml)
- **Testing**: [testify v1.10.0](https://github.com/stretchr/testify)
- **Container**: Distroless Debian 12 (coming in M3)

---

## 🎯 Use Cases

### 1. Debugging Stuck Training Jobs

```
SRE: "Why is the training job on node-5 stuck?"
Claude → k8s-gpu-mcp-server → Detects XID 48 (ECC Error)
Claude: "Node-5 has uncorrectable memory errors. Drain immediately."
```

### 2. Thermal Management

```
SRE: "Are any GPUs thermal throttling?"
Claude → k8s-gpu-mcp-server → Checks temps and throttle status
Claude: "GPU 3 is at 86°C and thermal throttling. Check cooling."
```

### 3. Topology Validation

```
SRE: "Is NVLink properly configured for multi-GPU training?"
Claude → k8s-gpu-mcp-server → Inspects NVLink topology
Claude: "All 8 GPUs connected via NVLink, 600GB/s bandwidth."
```

### 4. Zombie Process Hunting

```
SRE: "GPU memory is full but no pods are running"
Claude → k8s-gpu-mcp-server → Lists GPU processes
Claude: "Found zombie process PID 12345 using 8GB. Kill it?"
```

---

## 🏆 Achievements

- ✅ **Go 1.25** - Latest Go version
- ✅ **Real NVML** - Tested on Tesla T4
- ✅ **550+ Tests Passing** - Race detector enabled, 58-80% coverage
- ✅ **HTTP-First Architecture** - 150× faster than exec routing
- ✅ **Gateway + Circuit Breaker** - Production-grade reliability
- ✅ **MCP Prompts** - Guided diagnostic workflows for SRE troubleshooting
- ✅ **Prometheus Metrics** - Per-node latency tracking
- ✅ **~8MB Binary** - 84% under 50MB target
- ✅ **MCP 2025-06-18** - Latest protocol version

---

## 📄 License

Apache License 2.0 - See [LICENSE](LICENSE) for details.

---

## 🙏 Acknowledgments

- [NVIDIA NVML](https://developer.nvidia.com/nvidia-management-library-nvml) - GPU Management Library
- [Model Context Protocol](https://modelcontextprotocol.io/) - MCP Specification
- [mcp-go](https://github.com/mark3labs/mcp-go) - MCP Go Implementation
- [Anthropic Claude](https://www.anthropic.com/claude) - AI Assistant
- [Cursor](https://cursor.sh/) - AI-Powered IDE

---

## 📞 Contact

**Maintainer:** [@ArangoGutierrez](https://github.com/ArangoGutierrez)  
**Issues:** [GitHub Issues](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/issues)  
**Discussions:** [GitHub Discussions](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/discussions)

---

<div align="center">

**⭐ Star us on GitHub — it helps!**

[Report Bug](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/issues/new?template=bug_report.yml) · 
[Request Feature](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/issues/new?template=feature_request.yml) · 
[View Roadmap](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/milestones)

</div>
