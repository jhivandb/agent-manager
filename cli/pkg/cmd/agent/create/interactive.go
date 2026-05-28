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
