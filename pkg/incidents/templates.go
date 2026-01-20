// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

// explanationTemplates maps causality types to explanation templates.
// Templates use Go text/template syntax with ExplanationData as context.
var explanationTemplates = map[string]string{
	"hardware_thermal": `Your {{if .PodName}}{{.PodName}}{{else}}workload{{end}} job failed due to GPU overheating on Node {{.Node}}, not a bug in your code.

The GPU reached {{.Temperature}}°C (shutdown threshold: {{.TempThreshold}}°C), triggering a hardware reset{{if .XIDCode}} (XID {{.XIDCode}}){{end}}.{{if .ThrottleDuration}} The GPU was thermally throttling for {{.ThrottleDuration}} before the failure.{{end}}

**This is a hardware/infrastructure issue, not your code.**
{{if .Timeline}}
## Timeline
{{range .Timeline}}- {{.Timestamp.Format "15:04:05"}} ({{.RelativeTime}}) - {{.Description}}
{{end}}{{end}}
## Recommended Actions
1. **High Priority**: Cordon the node
   ` + "`" + `kubectl cordon {{.Node}}` + "`" + `

2. **High Priority**: Report to datacenter ops for cooling check

3. **Medium Priority**: Restart your job - it should succeed on a different node`,

	"hardware_memory": `Your {{if .PodName}}{{.PodName}}{{else}}workload{{end}} job encountered a GPU memory hardware failure on Node {{.Node}}.

{{.ECCUncorrectable}} uncorrectable ECC errors were detected, indicating failing GPU memory.{{if .XIDCode}} This triggered XID {{.XIDCode}}{{if .XIDDescription}} ({{.XIDDescription}}){{end}}.{{end}}

**This is a hardware failure requiring GPU replacement, not a problem with your code.**
{{if .Timeline}}
## Timeline
{{range .Timeline}}- {{.Timestamp.Format "15:04:05"}} ({{.RelativeTime}}) - {{.Description}}
{{end}}{{end}}
## Recommended Actions
1. **High Priority**: Drain the node
   ` + "`" + `kubectl drain {{.Node}} --ignore-daemonsets` + "`" + `

2. **High Priority**: Schedule GPU replacement

3. **Medium Priority**: Restart your job on a different node`,

	"software_oom": `Your {{if .PodName}}{{.PodName}}{{else}}workload{{end}} job ran out of GPU memory.

Peak memory usage was {{.MemUsed | bytes}} of {{.MemTotal | bytes}} available ({{printf "%.1f" .MemPercent}}%).

**This is likely a code/configuration issue.** Consider:
- Reducing batch size
- Enabling gradient checkpointing
- Using a GPU with more memory
{{if .Timeline}}
## Timeline
{{range .Timeline}}- {{.Timestamp.Format "15:04:05"}} ({{.RelativeTime}}) - {{.Description}}
{{end}}{{end}}
## Recommended Actions
1. **High Priority**: Review memory usage in your training script

2. **Medium Priority**: Try reducing batch size by 50%

3. **Medium Priority**: Consider requesting a GPU with more memory`,

	"unknown": `Your {{if .PodName}}{{.PodName}}{{else}}workload{{end}} job experienced a GPU-related failure on Node {{.Node}}.

{{if .XIDCode}}An XID {{.XIDCode}} error was detected{{if .XIDDescription}}: {{.XIDDescription}}{{end}}.{{end}}

The exact root cause could not be automatically determined. Manual investigation recommended.
{{if .Timeline}}
## Timeline
{{range .Timeline}}- {{.Timestamp.Format "15:04:05"}} ({{.RelativeTime}}) - {{.Description}}
{{end}}{{end}}
## Recommended Actions
1. **High Priority**: Review the timeline above for patterns

2. **Medium Priority**: Check GPU health
   ` + "`" + `nvidia-smi -q` + "`" + `

3. **Medium Priority**: Review application logs for errors`,
}

// summaryTemplates provides one-line summaries for each causality type.
var summaryTemplates = map[string]string{
	"hardware_thermal": `GPU overheating on {{.Node}} caused {{if .PodName}}{{.PodName}}{{else}}your workload{{end}} to fail (not your code).`,
	"hardware_memory":  `GPU memory hardware failure on {{.Node}} caused {{if .PodName}}{{.PodName}}{{else}}your workload{{end}} to fail (not your code).`,
	"software_oom":     `{{if .PodName}}{{.PodName}}{{else}}Your workload{{end}} ran out of GPU memory ({{printf "%.0f" .MemPercent}}% used).`,
	"unknown":          `GPU failure detected on {{.Node}} - manual investigation recommended.`,
}
