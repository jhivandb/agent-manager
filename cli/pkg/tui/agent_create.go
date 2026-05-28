// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// Licensed under the Apache License, Version 2.0 (the "License"). See the
// header in tui.go for the full notice.

package tui

import (
	"strings"

	"github.com/charmbracelet/huh"

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
func RunAgentCreateForm(in AgentCreateInput) (AgentCreateInput, error) {
	out := in

	// Pre-populate defaults *into the bound values* — huh.Input.Placeholder()
	// only paints grey hint text and does NOT seed Value(&x). Without this,
	// users would see "main" / "." / "Dockerfile" in the field, accept them,
	// and then fail validation because the underlying string is still empty.
	out.RepoBranch = defaultString(out.RepoBranch, "main")
	out.RepoPath = defaultString(out.RepoPath, ".")
	out.Dockerfile = defaultString(out.Dockerfile, "Dockerfile")
	if out.Port == 0 {
		out.Port = 8000
	}

	form := huh.NewForm(
		identityGroup(&out),
		provisioningGroup(&out),
		subTypeGroup(&out),
		repositoryGroup(&out),
		buildTypeGroup(&out),
		buildpackDetailsGroup(&out),
		dockerDetailsGroup(&out),
	)

	if err := form.Run(); err != nil {
		return out, err
	}
	return out, nil
}

func defaultString(current, fallback string) string {
	if strings.TrimSpace(current) == "" {
		return fallback
	}
	return current
}

func identityGroup(out *AgentCreateInput) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Agent name").
			Description("Lowercase, no '/'. Used as the resource identifier.").
			Value(&out.Name).
			Validate(func(s string) error {
				s = strings.TrimSpace(s)
				if s == "" {
					return errRequired("agent name")
				}
				if strings.Contains(s, "/") {
					return errInvalid("agent name must not contain '/'")
				}
				return nil
			}),
		huh.NewInput().
			Title("Display name").
			Value(&out.DisplayName).
			Validate(requireNonEmpty("display name")),
		huh.NewText().
			Title("Description").
			Description("Optional. Free text shown in listings.").
			Value(&out.Description),
	)
}

func provisioningGroup(out *AgentCreateInput) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Provisioning mode").
			Options(
				huh.NewOption("internal — managed build & deploy", provisioningInternal),
				huh.NewOption("external — bring your own runtime", provisioningExternal),
			).
			Value(&out.Provisioning),
	)
}

// --- validator helpers ---

type formError struct{ msg string }

func (e formError) Error() string { return e.msg }

func errRequired(name string) error { return formError{msg: name + " is required"} }
func errInvalid(msg string) error   { return formError{msg: msg} }

func requireNonEmpty(name string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errRequired(name)
		}
		return nil
	}
}

func subTypeGroup(out *AgentCreateInput) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Agent sub-type").
			Description("chat-api exposes a fixed chat endpoint; custom-api lets you define your own HTTP surface.").
			Options(
				huh.NewOption("chat-api", subTypeChatAPI),
				huh.NewOption("custom-api", subTypeCustomAPI),
			).
			Value(&out.SubType),
	).WithHideFunc(func() bool { return out.Provisioning != provisioningInternal })
}

func repositoryGroup(out *AgentCreateInput) *huh.Group {
	// RepoBranch / RepoPath were pre-seeded with "main" / "." in
	// RunAgentCreateForm so the bound values are populated; the form merely
	// presents and validates them.
	return huh.NewGroup(
		huh.NewInput().
			Title("Repository URL").
			Description("HTTPS or SSH URL of the source repository.").
			Value(&out.RepoURL).
			Validate(requireNonEmpty("repository URL")),
		huh.NewInput().
			Title("Branch").
			Value(&out.RepoBranch).
			Validate(requireNonEmpty("branch")),
		huh.NewInput().
			Title("Path within the repository").
			Value(&out.RepoPath).
			Validate(requireNonEmpty("repository path")),
		huh.NewInput().
			Title("Secret reference (optional)").
			Description("Name of a previously-created secret for private repositories. Leave blank for public repos.").
			Value(&out.RepoSecret),
	).WithHideFunc(func() bool { return out.Provisioning != provisioningInternal })
}

func buildTypeGroup(out *AgentCreateInput) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Build type").
			Options(
				huh.NewOption("buildpack — language-based detection", buildTypeBuildpack),
				huh.NewOption("docker — your own Dockerfile", buildTypeDocker),
			).
			Value(&out.BuildType),
	).WithHideFunc(func() bool { return out.Provisioning != provisioningInternal })
}

func buildpackDetailsGroup(out *AgentCreateInput) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Language").
			Description("e.g. go, python, nodejs, java").
			Value(&out.Language).
			Validate(requireNonEmpty("language")),
		huh.NewInput().
			Title("Language version").
			Value(&out.LanguageVersion).
			Validate(requireNonEmpty("language version")),
		huh.NewInput().
			Title("Run command").
			Description("Command used to start the agent in the built image.").
			Value(&out.RunCommand).
			Validate(requireNonEmpty("run command")),
	).WithHideFunc(func() bool {
		return out.Provisioning != provisioningInternal || out.BuildType != buildTypeBuildpack
	})
}

func dockerDetailsGroup(out *AgentCreateInput) *huh.Group {
	// out.Dockerfile pre-seeded to "Dockerfile" in RunAgentCreateForm.
	return huh.NewGroup(
		huh.NewInput().
			Title("Dockerfile path").
			Value(&out.Dockerfile).
			Validate(requireNonEmpty("Dockerfile path")),
	).WithHideFunc(func() bool {
		return out.Provisioning != provisioningInternal || out.BuildType != buildTypeDocker
	})
}
