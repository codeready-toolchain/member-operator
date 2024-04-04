package deploy

import "embed"

//go:embed templates/toolchaincluster/*
var ToolchainClusterTemplateFS embed.FS
