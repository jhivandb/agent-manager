// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// Licensed under the Apache License, Version 2.0 (the "License"). See the
// header in tui.go for the full notice.

package tui

import (
	amsvc "github.com/wso2/agent-manager/cli/pkg/clients/amsvc/gen"
)

// Package-private string values used by the agent-create form. Provisioning
// and build-type values are sourced from the generated amsvc enums so they
// cannot drift from the wire contract; subtype values are CLI-only literals
// that mirror the unexported constants in cli/pkg/cmd/agent/create. Keeping
// these unexported preserves pkg/tui's public surface (AgentCreateInput +
// RunAgentCreateForm only).
const (
	provisioningInternal = string(amsvc.ProvisioningTypeInternal)
	provisioningExternal = string(amsvc.ProvisioningTypeExternal)

	buildTypeBuildpack = string(amsvc.Buildpack)
	buildTypeDocker    = string(amsvc.Docker)

	subTypeChatAPI   = "chat-api"
	subTypeCustomAPI = "custom-api"
)

// AgentCreateInput carries the "core" fields of `amctl agent create` between
// the command and the huh? form. Fields outside this set (env vars, OpenAPI
// spec, model config, auto-instrumentation) remain flag-only and are not
// touched by the form.
type AgentCreateInput struct {
	Name        string
	DisplayName string
	Description string

	Provisioning string // "internal" | "external"
	SubType      string // "chat-api" | "custom-api"

	RepoURL    string
	RepoBranch string
	RepoPath   string
	RepoSecret string

	BuildType       string // "buildpack" | "docker"
	Language        string
	LanguageVersion string
	RunCommand      string
	Dockerfile      string

	Port     int
	PortSet  bool
	BasePath string
}

// RunAgentCreateForm renders the agent-create wizard and returns the user's
// final selections. Cancellation surfaces as huh.ErrUserAborted, which the
// caller in package create maps to clierr.ConfirmationRequired.
//
// This is a stub that returns the input unchanged. The real form body is
// implemented in subsequent tasks.
func RunAgentCreateForm(in AgentCreateInput) (AgentCreateInput, error) {
	return in, nil
}
