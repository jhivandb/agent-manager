// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// Licensed under the Apache License, Version 2.0 (the "License"). See the
// LICENSE notice in create.go for the full text.

package create

import (
	"github.com/wso2/agent-manager/cli/pkg/tui"
)

// shouldRunForm reports whether the interactive form should be invoked given
// the current IOStreams and the set of fields already supplied via flags.
// The predicate is a pure function on *CreateOptions so it can be exercised
// in unit tests.
func shouldRunForm(opts *CreateOptions) bool {
	if !opts.IO.CanPrompt() {
		return false
	}
	return hasMissingRequired(opts)
}

func hasMissingRequired(opts *CreateOptions) bool {
	// Dispatch on provisioning first so an unknown value short-circuits to
	// false and lets validate() (validation.go:46-48) surface the invalid
	// --provisioning to the user. The CLI defaults --provisioning to
	// "internal" in NewCreateCmd, so empty is treated as internal.
	switch opts.Provisioning {
	case "", provisioningInternal:
		// fall through to internal-required checks
	case provisioningExternal:
		return opts.Name == "" || opts.DisplayName == ""
	default:
		return false
	}

	if opts.Name == "" || opts.DisplayName == "" {
		return true
	}
	if opts.SubType == "" {
		return true
	}
	if opts.RepoURL == "" || opts.RepoBranch == "" || opts.RepoPath == "" {
		return true
	}
	if opts.BuildType == "" {
		return true
	}
	switch opts.BuildType {
	case buildTypeBuildpack:
		if opts.Language == "" || opts.LanguageVersion == "" || opts.RunCommand == "" {
			return true
		}
	case buildTypeDocker:
		if opts.Dockerfile == "" {
			return true
		}
	}
	if opts.SubType == subTypeCustomAPI && opts.BasePath == "" {
		return true
	}
	return false
}

// runAgentCreateForm is the form-runner seam. Tests swap this with a stub.
var runAgentCreateForm = tui.RunAgentCreateForm

func agentCreateInputFromOpts(opts *CreateOptions) tui.AgentCreateInput {
	return tui.AgentCreateInput{
		Name:            opts.Name,
		DisplayName:     opts.DisplayName,
		Description:     opts.Description,
		Provisioning:    opts.Provisioning,
		SubType:         opts.SubType,
		RepoURL:         opts.RepoURL,
		RepoBranch:      opts.RepoBranch,
		RepoPath:        opts.RepoPath,
		RepoSecret:      opts.RepoSecret,
		BuildType:       opts.BuildType,
		Language:        opts.Language,
		LanguageVersion: opts.LanguageVersion,
		RunCommand:      opts.RunCommand,
		Dockerfile:      opts.Dockerfile,
		Port:            opts.Port,
		PortSet:         opts.PortSet,
		BasePath:        opts.BasePath,
	}
}

// applyAgentCreateInput writes the form's "core" output back onto opts.
// Fields outside the core set (env vars, OpenAPISpec, ModelConfigFile,
// DisableAutoInstrumentation) are intentionally untouched.
//
// Two clamps are required for the result to pass validate():
//   - PortSet must be false for chat-api (validation.go:109-111).
//   - When the user lands on external provisioning, internal-only fields
//     must be cleared (validation.go:184-197 rejects any of them).
func applyAgentCreateInput(opts *CreateOptions, in tui.AgentCreateInput) {
	opts.Name = in.Name
	opts.DisplayName = in.DisplayName
	opts.Description = in.Description
	opts.Provisioning = in.Provisioning

	if in.Provisioning == provisioningExternal {
		opts.SubType = ""
		opts.RepoURL = ""
		opts.RepoBranch = ""
		opts.RepoPath = ""
		opts.RepoSecret = ""
		opts.BuildType = ""
		opts.Language = ""
		opts.LanguageVersion = ""
		opts.RunCommand = ""
		opts.Dockerfile = ""
		opts.BasePath = ""
		opts.PortSet = false
		// Leave opts.Port at whatever value it had — it has no effect when
		// PortSet=false and external provisioning explicitly disallows --port.
		return
	}

	opts.SubType = in.SubType
	opts.RepoURL = in.RepoURL
	opts.RepoBranch = in.RepoBranch
	opts.RepoPath = in.RepoPath
	opts.RepoSecret = in.RepoSecret
	opts.BuildType = in.BuildType
	opts.Language = in.Language
	opts.LanguageVersion = in.LanguageVersion
	opts.RunCommand = in.RunCommand
	opts.Dockerfile = in.Dockerfile
	opts.Port = in.Port
	opts.BasePath = in.BasePath

	switch in.SubType {
	case subTypeCustomAPI:
		opts.PortSet = true
	case subTypeChatAPI:
		opts.PortSet = false
	default:
		opts.PortSet = in.PortSet
	}
}
