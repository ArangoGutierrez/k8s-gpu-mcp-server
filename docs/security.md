# Security Model

This document describes the security architecture, RBAC requirements, and
best practices for deploying k8s-gpu-mcp-server.

## Table of Contents

- [Principle of Least Privilege](#principle-of-least-privilege)
- [Component Permissions](#component-permissions)
- [RBAC Configuration](#rbac-configuration)
- [Security Contexts](#security-contexts)
- [Network Security](#network-security)
- [Capability Requirements](#capability-requirements)
- [Graceful Permission Failures](#graceful-permission-failures)
- [Verification](#verification)

## Principle of Least Privilege

The agent follows strict least-privilege principles:

| Principle | Implementation |
|-----------|----------------|
| **No Secrets Access** | Agent never reads K8s secrets |
| **No Exec** | Agent doesn't exec into other pods |
| **No Mutations** | Read-only mode is default |
| **Minimal Network** | No listening ports in stdio mode |
| **No Privilege Escalation** | `allowPrivilegeEscalation: false` |

## Component Permissions

### Agent DaemonSet

The agent needs K8s API access for two tools:

| Tool | Resources | Verbs | Scope |
|------|-----------|-------|-------|
| `describe_gpu_node` | `nodes` | `get`, `list` | Cluster |
| `get_pod_gpu_allocation` | `pods` | `get`, `list` | Cluster |

All other tools (`get_gpu_inventory`, `get_gpu_health`, `analyze_xid_errors`)
use only local NVML or `/dev/kmsg` access—no K8s API required.

### Gateway

The gateway requires additional permissions for multi-node routing:

| Purpose | Resources | Verbs |
|---------|-----------|-------|
| Discover agent pods | `pods` | `get`, `list` |
| Exec routing (stdio mode) | `pods/exec` | `create` |
| Node info aggregation | `nodes` | `get`, `list` |

## RBAC Configuration

### Helm (Recommended)

```bash
# Default: agent RBAC enabled
helm install gpu-mcp ./deployment/helm/k8s-gpu-mcp-server

# Gateway-only mode (agents access via gateway, may not need agent RBAC)
helm install gpu-mcp ./deployment/helm/k8s-gpu-mcp-server \
  --set gateway.enabled=true

# Disable agent RBAC (if agents only need local NVML access)
helm install gpu-mcp ./deployment/helm/k8s-gpu-mcp-server \
  --set agent.rbac.create=false

# Operator mode (includes pod eviction permissions)
helm install gpu-mcp ./deployment/helm/k8s-gpu-mcp-server \
  --set agent.mode=operator
```

### Standalone Manifests

```bash
# Read-only mode (default, recommended)
kubectl apply -f deployment/rbac/agent-rbac-readonly.yaml

# Namespace-scoped (multi-tenant environments)
kubectl apply -f deployment/rbac/agent-rbac-namespaced.yaml

# Operator mode (includes pod eviction for future tools)
kubectl apply -f deployment/rbac/agent-rbac-operator.yaml
```

### RBAC Manifest Comparison

| Manifest | Scope | Nodes | Pods | Eviction | Use Case |
|----------|-------|-------|------|----------|----------|
| `agent-rbac-readonly.yaml` | Cluster | ✓ | ✓ | ✗ | Default, full monitoring |
| `agent-rbac-namespaced.yaml` | Namespace | ✗ | ✓ | ✗ | Multi-tenant, restricted |
| `agent-rbac-operator.yaml` | Cluster | ✓ | ✓ | ✓ | Active management |

## Security Contexts

### Agent Container

```yaml
securityContext:
  runAsUser: 0                    # May be required for NVML
  allowPrivilegeEscalation: false # Prevent privilege escalation
  readOnlyRootFilesystem: true    # Immutable container
  capabilities:
    drop: ["ALL"]
    add:
      - SYSLOG                    # Required for /dev/kmsg (XID errors)
```

**Note**: Running as root may be required for NVML access depending on your
GPU driver configuration. The `SYSLOG` capability is required to read
`/dev/kmsg` for XID error analysis.

### Gateway Container

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65534                # nobody
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
```

The gateway doesn't need elevated privileges since it only makes K8s API
calls and HTTP requests to agent pods.

## Network Security

### NetworkPolicy (Optional)

Restrict agent communication to only allow gateway access:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: k8s-gpu-mcp-server-agent
  namespace: gpu-diagnostics
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: k8s-gpu-mcp-server
      app.kubernetes.io/component: gpu-diagnostics
  policyTypes:
    - Ingress
  ingress:
    # Only allow gateway to connect to agents
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/component: gateway
      ports:
        - port: 8080
          protocol: TCP
```

Enable in Helm:

```bash
helm install gpu-mcp ./deployment/helm/k8s-gpu-mcp-server \
  --set networkPolicy.enabled=true
```

## Capability Requirements

| Capability | Required For | When |
|------------|--------------|------|
| `CAP_SYSLOG` | `/dev/kmsg` read for XID errors | Always (for `analyze_xid_errors`) |
| `CAP_SYS_ADMIN` | GPU profiling metrics | Optional (advanced monitoring) |

The agent drops all capabilities by default and only adds `CAP_SYSLOG` for
XID error detection from the kernel ring buffer.

## Graceful Permission Failures

The agent handles missing RBAC permissions gracefully rather than failing:

### describe_gpu_node

If the agent lacks `nodes: get` permission, it returns NVML-only data:

```json
{
  "status": "partial",
  "node": {
    "name": "gpu-node-1",
    "labels": {},
    "k8s_unavailable": true,
    "k8s_error": "nodes \"gpu-node-1\" is forbidden: ..."
  },
  "driver": { "version": "535.154.05" },
  "gpus": [...]
}
```

### get_pod_gpu_allocation

If the agent lacks `pods: list` permission, it returns an error with
troubleshooting guidance:

```json
{
  "status": "error",
  "error": "failed to list pods: pods is forbidden",
  "hint": "Agent may lack RBAC permissions. Apply deployment/rbac/agent-rbac-readonly.yaml"
}
```

## Verification

Test RBAC permissions after deployment:

```bash
# Check agent can read nodes
kubectl auth can-i get nodes \
  --as=system:serviceaccount:gpu-diagnostics:k8s-gpu-mcp-server

# Check agent can list pods
kubectl auth can-i list pods \
  --as=system:serviceaccount:gpu-diagnostics:k8s-gpu-mcp-server

# Verify agent cannot access secrets (should return "no")
kubectl auth can-i get secrets \
  --as=system:serviceaccount:gpu-diagnostics:k8s-gpu-mcp-server

# Verify agent cannot exec into pods (should return "no")
kubectl auth can-i create pods/exec \
  --as=system:serviceaccount:gpu-diagnostics:k8s-gpu-mcp-server
```

### Validate RBAC Manifests

```bash
# Validate standalone manifests (dry-run)
kubectl apply --dry-run=client -f deployment/rbac/

# Validate Helm templates (dry-run)
helm template test ./deployment/helm/k8s-gpu-mcp-server \
  --set agent.rbac.create=true | kubectl apply --dry-run=client -f -

# Use Makefile target
make validate-rbac
```

## Gateway High Availability

The gateway is the single entry point for all MCP clients. For production deployments,
when the gateway is enabled, high availability configuration is used by default.

### Default HA Configuration

| Setting | Value | Purpose |
|---------|-------|---------|
| `gateway.replicas` | 2 | Eliminates single point of failure |
| PodDisruptionBudget | Auto-created | Protects during voluntary disruptions |
| Pod Anti-Affinity | Soft preference | Spreads replicas across nodes |

### Production vs Development

| Setting | Development | Production |
|---------|-------------|------------|
| `gateway.replicas` | 1 | 2+ (default) |
| PDB | Not created | Auto-created |
| Anti-affinity | Not applied | Applied |

To disable HA for development/testing:

```bash
helm install gpu-mcp ./deployment/helm/k8s-gpu-mcp-server \
  --set gateway.enabled=true \
  --set gateway.replicas=1
```

### Verify HA Configuration

```bash
# Check replicas
kubectl get deployment -n gpu-diagnostics \
  -l app.kubernetes.io/component=gateway

# Check PDB
kubectl get pdb -n gpu-diagnostics

# Simulate pod failure (gateway should remain available)
POD=$(kubectl get pods -n gpu-diagnostics \
  -l app.kubernetes.io/component=gateway \
  --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')
kubectl delete pod -n gpu-diagnostics "$POD"
```

## Troubleshooting

### "nodes is forbidden" Error

The agent's ServiceAccount lacks `nodes: get` permission.

**Solution**: Apply the read-only RBAC manifest:

```bash
kubectl apply -f deployment/rbac/agent-rbac-readonly.yaml
```

### "pods is forbidden" Error

The agent's ServiceAccount lacks `pods: list` permission.

**Solution**: Same as above, apply the RBAC manifest.

### Agent Returns Partial Data

This is expected behavior when RBAC permissions are limited. The agent
returns what it can access (NVML data) without failing completely.

### Gateway Cannot Exec to Agents

The gateway's ServiceAccount lacks `pods/exec: create` permission.

**Solution**: Ensure gateway RBAC is applied (included in Helm by default):

```bash
helm upgrade --install gpu-mcp ./deployment/helm/k8s-gpu-mcp-server \
  --set gateway.enabled=true
```

## Security Best Practices

1. **Start with read-only mode** - Only enable operator mode when needed
2. **Use Helm** - Ensures consistent RBAC across environments
3. **Enable NetworkPolicy** - Restrict agent communication in production
4. **Audit permissions** - Regularly verify with `kubectl auth can-i`
5. **Monitor API server logs** - Watch for forbidden access attempts
