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

// Note: tui import is used by later tests in this file.
var _ = tui.AgentCreateInput{}
