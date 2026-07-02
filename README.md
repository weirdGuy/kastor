# agentform

Agentform is "Terraform for AI agents." It provides a vendor-neutral, versionable spec (.agent, .tool, .prompt files in HCL) and a Go toolchain with two paths: agentform build generates runnable projects for frameworks like LangGraph, and agentform plan/apply reconciles agents as long-lived resources on hosted platforms like OpenAI Assistants – with state, diffs, and drift detection.