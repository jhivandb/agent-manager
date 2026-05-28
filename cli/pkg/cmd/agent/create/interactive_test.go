// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// Licensed under the Apache License, Version 2.0 (the "License"). See the
// LICENSE notice in create.go for the full text.

package create

import (
	"testing"

	"github.com/wso2/agent-manager/cli/pkg/iostreams"
	"github.com/wso2/agent-manager/cli/pkg/tui"
)

func newIOWithPrompt(canPrompt bool) *iostreams.IOStreams {
	ios, _, _, _ := iostreams.Test()
	// CanPrompt() == stdinIsTerminal && stderrIsTerminal.
	ios.SetTerminal(canPrompt, canPrompt, canPrompt)
	return ios
}

func fullyPopulatedInternalOpts() *CreateOptions {
	return &CreateOptions{
		IO:              newIOWithPrompt(true),
		Name:            "my-agent",
		DisplayName:     "My Agent",
		Provisioning:    provisioningInternal,
		SubType:         subTypeCustomAPI,
		RepoURL:         "https://example.com/repo",
		RepoBranch:      "main",
		RepoPath:        ".",
		BuildType:       buildTypeBuildpack,
		Language:        "go",
		LanguageVersion: "1.22",
		RunCommand:      "go run .",
		BasePath:        "/api",
		Port:            8000,
	}
}

func fullyPopulatedExternalOpts() *CreateOptions {
	return &CreateOptions{
		IO:           newIOWithPrompt(true),
		Name:         "my-agent",
		DisplayName:  "My Agent",
		Provisioning: provisioningExternal,
	}
}

func TestShouldRunForm_NoTTY(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.IO = newIOWithPrompt(false)
	opts.DisplayName = "" // missing required field
	if shouldRunForm(opts) {
		t.Fatalf("expected false when CanPrompt() is false even with missing fields")
	}
}

func TestShouldRunForm_AllFieldsPresent_Internal(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	if shouldRunForm(opts) {
		t.Fatalf("expected false when no required fields are missing")
	}
}

func TestShouldRunForm_AllFieldsPresent_External(t *testing.T) {
	opts := fullyPopulatedExternalOpts()
	if shouldRunForm(opts) {
		t.Fatalf("expected false for fully-populated external opts")
	}
}

func TestShouldRunForm_MissingDisplayName(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.DisplayName = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when --display-name is missing")
	}
}

func TestShouldRunForm_MissingName(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.Name = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when name argument is missing")
	}
}

func TestShouldRunForm_InternalMissingSubType(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.SubType = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when --subtype is missing for internal")
	}
}

func TestShouldRunForm_InternalMissingBuildType(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.BuildType = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when --build-type is missing for internal")
	}
}

func TestShouldRunForm_InternalMissingRepoURL(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.RepoURL = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when --repo-url is missing for internal")
	}
}

func TestShouldRunForm_InternalBuildpackMissingLanguage(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.Language = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when buildpack language is missing")
	}
}

func TestShouldRunForm_InternalDockerNeedsDockerfile(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.BuildType = buildTypeDocker
	opts.Language = ""
	opts.LanguageVersion = ""
	opts.RunCommand = ""
	opts.Dockerfile = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when docker --dockerfile is missing")
	}
}

func TestShouldRunForm_CustomAPIMissingBasePath(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.BasePath = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when --base-path is missing for custom-api")
	}
}

func TestShouldRunForm_EmptyProvisioningTreatedAsInternal(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.Provisioning = ""
	opts.SubType = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when provisioning is empty and required internal fields are missing")
	}
}

func TestShouldRunForm_UnknownProvisioningFallsThroughToValidate(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.Provisioning = "cloud" // invalid; validate() will reject
	opts.DisplayName = ""       // would normally trigger the form
	if shouldRunForm(opts) {
		t.Fatalf("expected false for unknown provisioning value; validate() must surface it")
	}
}

func TestShouldRunForm_ExternalOnlyChecksIdentity(t *testing.T) {
	opts := fullyPopulatedExternalOpts()
	opts.SubType = "" // not required for external; should not trigger
	opts.RepoURL = ""
	if shouldRunForm(opts) {
		t.Fatalf("expected false: external mode only cares about identity fields")
	}
	opts.DisplayName = ""
	if !shouldRunForm(opts) {
		t.Fatalf("expected true when external --display-name is missing")
	}
}

func TestAgentCreateInputFromOpts_RoundTrip(t *testing.T) {
	opts := fullyPopulatedInternalOpts()
	opts.PortSet = true
	opts.Description = "desc"
	opts.RepoSecret = "secret-ref"
	opts.Dockerfile = "Dockerfile.prod" // ignored: BuildType is buildpack
	in := agentCreateInputFromOpts(opts)

	if in.Name != "my-agent" || in.DisplayName != "My Agent" || in.Description != "desc" {
		t.Errorf("identity fields lost: %+v", in)
	}
	if in.Provisioning != provisioningInternal || in.SubType != subTypeCustomAPI {
		t.Errorf("agent shape lost: %+v", in)
	}
	if in.RepoURL == "" || in.RepoBranch == "" || in.RepoPath == "" || in.RepoSecret != "secret-ref" {
		t.Errorf("repo fields lost: %+v", in)
	}
	if in.BuildType != buildTypeBuildpack || in.Language != "go" || in.LanguageVersion != "1.22" || in.RunCommand != "go run ." {
		t.Errorf("build fields lost: %+v", in)
	}
	if in.Port != 8000 || !in.PortSet || in.BasePath != "/api" {
		t.Errorf("service-shape fields lost: %+v", in)
	}
}

func TestApplyAgentCreateInput_OverwritesCoreFieldsOnly(t *testing.T) {
	opts := &CreateOptions{
		Name:                       "stale",
		DisplayName:                "Stale",
		Description:                "stale-desc",
		Provisioning:               provisioningInternal,
		SubType:                    subTypeChatAPI,
		Env:                        []string{"KEEP=1"},
		EnvSecret:                  []string{"KEEPSEC=1"},
		EnvFromSecret:              []string{"KEEPFS=ref"},
		OpenAPISpec:                "/keep/openapi.yaml",
		ModelConfigFile:            "/keep/model.yaml",
		DisableAutoInstrumentation: true,
	}
	out := tui.AgentCreateInput{
		Name:            "new-agent",
		DisplayName:     "New",
		Description:     "new-desc",
		Provisioning:    provisioningInternal,
		SubType:         subTypeCustomAPI,
		RepoURL:         "https://example.com/r",
		RepoBranch:      "main",
		RepoPath:        ".",
		BuildType:       buildTypeBuildpack,
		Language:        "go",
		LanguageVersion: "1.22",
		RunCommand:      "go run .",
		Port:            8080,
		BasePath:        "/api",
	}
	applyAgentCreateInput(opts, out)

	if opts.Name != "new-agent" || opts.DisplayName != "New" || opts.SubType != subTypeCustomAPI {
		t.Errorf("core fields not overwritten: %+v", opts)
	}
	if len(opts.Env) != 1 || opts.Env[0] != "KEEP=1" {
		t.Errorf("Env outside-of-scope was mutated: %+v", opts.Env)
	}
	if opts.OpenAPISpec != "/keep/openapi.yaml" || opts.ModelConfigFile != "/keep/model.yaml" {
		t.Errorf("path fields outside scope mutated: %+v", opts)
	}
	if !opts.DisableAutoInstrumentation {
		t.Errorf("DisableAutoInstrumentation outside scope was mutated")
	}
}

func TestApplyAgentCreateInput_PortSetCustomAPI(t *testing.T) {
	opts := &CreateOptions{}
	applyAgentCreateInput(opts, tui.AgentCreateInput{
		Provisioning: provisioningInternal,
		SubType:      subTypeCustomAPI,
		Port:         8080,
	})
	if !opts.PortSet {
		t.Errorf("expected PortSet=true for custom-api")
	}
	if opts.Port != 8080 {
		t.Errorf("expected Port=8080, got %d", opts.Port)
	}
}

func TestApplyAgentCreateInput_PortSetClampedForChatAPI(t *testing.T) {
	opts := &CreateOptions{PortSet: true, Port: 8000}
	applyAgentCreateInput(opts, tui.AgentCreateInput{
		Provisioning: provisioningInternal,
		SubType:      subTypeChatAPI,
		Port:         8000,
		PortSet:      true, // user toggled custom-api then switched back; form may leave this set
	})
	if opts.PortSet {
		t.Errorf("expected PortSet=false for chat-api regardless of input")
	}
}

func TestApplyAgentCreateInput_ExternalClearsInternalFields(t *testing.T) {
	// If the user filled internal-mode groups and then switched provisioning
	// back to "external", the hidden internal values must be cleared.
	// validateExternal at validation.go:184-197 rejects every internal-only
	// field, so leaving leftover values causes a validation failure even
	// though the form did not surface them.
	opts := &CreateOptions{
		SubType:         subTypeChatAPI,
		RepoURL:         "https://leftover/repo",
		RepoBranch:      "main",
		RepoPath:        ".",
		RepoSecret:      "secret",
		BuildType:       buildTypeBuildpack,
		Language:        "go",
		LanguageVersion: "1.22",
		RunCommand:      "go run .",
		Dockerfile:      "Dockerfile",
		Port:            8080,
		PortSet:         true,
		BasePath:        "/api",
	}
	applyAgentCreateInput(opts, tui.AgentCreateInput{
		Name:         "new",
		DisplayName:  "New",
		Provisioning: provisioningExternal,
	})
	if opts.Provisioning != provisioningExternal {
		t.Fatalf("expected external provisioning, got %q", opts.Provisioning)
	}
	if opts.SubType != "" || opts.RepoURL != "" || opts.RepoBranch != "" || opts.RepoPath != "" ||
		opts.RepoSecret != "" || opts.BuildType != "" || opts.Language != "" ||
		opts.LanguageVersion != "" || opts.RunCommand != "" || opts.Dockerfile != "" ||
		opts.BasePath != "" {
		t.Errorf("internal-only fields not cleared for external: %+v", opts)
	}
	if opts.PortSet {
		t.Errorf("PortSet must be cleared for external")
	}
}

// Note: tui import is used by later tests in this file.
var _ = tui.AgentCreateInput{}
