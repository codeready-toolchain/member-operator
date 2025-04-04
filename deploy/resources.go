package deploy

import "embed"

//go:embed templates/toolchaincluster/*
var ToolchainClusterTemplateFS embed.FS

//go:embed autoscaler/*
var AutoScalerFS embed.FS

//go:embed webhook/*
var WebhookFS embed.FS
