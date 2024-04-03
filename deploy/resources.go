package deploy

import "embed"

//go:embed toolchaincluster/*
var ToolchainClusterTemplateFS embed.FS
