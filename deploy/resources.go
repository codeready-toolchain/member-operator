package deploy

import "embed"

//go:embed templates/toolchaincluster/*
var ToolchainClusterTemplateFS embed.FS

//go:embed templates/autoscaler/*
var AutoScalerFS embed.FS

//go:embed templates/webhook/*
var WebhookFS embed.FS
