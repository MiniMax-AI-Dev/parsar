package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

const (
	testTenant      = "471e90e1-d9b3-418b-b339-928284514ae1"
	testKey         = "581d17e2-f063-42dc-b979-3fbd1c7a052c"
	testEnvironment = "5c9751a6-df59-4612-912e-55e6a7898c5a"
)

func principalArgs() []string {
	return []string{
		"--tenant", testTenant,
		"--organization", "test-org",
		"--project", "test-project",
		"--subject-kind", "service_account",
		"--subject-id", "test-executor",
		"--key-id", testKey,
	}
}

func TestParseOptionsPrincipalCredentialOperations(t *testing.T) {
	for _, test := range []struct {
		name        string
		args        []string
		environment string
		rotate      bool
		revoke      bool
	}{
		{name: "principal issuance"},
		{name: "exact issuance", args: []string{"--environment", testEnvironment}, environment: testEnvironment},
		{name: "rotation", args: []string{"--rotate"}, rotate: true},
		{name: "revocation", args: []string{"--revoke"}, revoke: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseOptions(append(principalArgs(), test.args...), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if options.principal.TenantID != testTenant || options.principal.OrganizationID != "test-org" ||
				options.principal.ProjectID != "test-project" || options.principal.SubjectKind != "service_account" ||
				options.principal.SubjectID != "test-executor" {
				t.Fatalf("unexpected principal: %+v", options.principal)
			}
			if options.keyID != testKey || options.environment != test.environment || options.rotate != test.rotate || options.revoke != test.revoke {
				t.Fatalf("unexpected operation: %+v", options)
			}
		})
	}
	options, err := parseOptions(append(principalArgs(), "--subject-kind", "user"), io.Discard)
	if err != nil || options.principal.SubjectKind != "user" {
		t.Fatalf("user principal rejected: %v", err)
	}
}

func TestParseOptionsRequiresEveryIdentityFlag(t *testing.T) {
	for _, name := range []string{"--tenant", "--organization", "--project", "--subject-kind", "--subject-id", "--key-id"} {
		t.Run(name, func(t *testing.T) {
			args := principalArgs()
			for i := 0; i < len(args); i += 2 {
				if args[i] == name {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			if _, err := parseOptions(args, io.Discard); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("missing %s was not clearly rejected: %v", name, err)
			}
		})
	}
	if _, err := parseOptions([]string{"--tenant", testTenant, "--environment", testEnvironment}, io.Discard); err == nil {
		t.Fatal("legacy exact-Environment arguments were accepted")
	}
}

func TestParseOptionsRejectsInvalidIdentityAndRestrictionChanges(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "conflicting operations", args: []string{"--rotate", "--revoke"}},
		{name: "rotation with restriction", args: []string{"--rotate", "--environment", testEnvironment}},
		{name: "revocation with restriction", args: []string{"--revoke", "--environment", testEnvironment}},
		{name: "rotation with empty restriction", args: []string{"--rotate", "--environment="}},
		{name: "revocation with empty restriction", args: []string{"--revoke", "--environment="}},
		{name: "empty issuance restriction", args: []string{"--environment="}},
		{name: "invalid environment", args: []string{"--environment", "invalid"}},
		{name: "noncanonical environment", args: []string{"--environment", strings.ToUpper(testEnvironment)}},
		{name: "zero environment", args: []string{"--environment", "00000000-0000-0000-0000-000000000000"}},
		{name: "invalid key", args: []string{"--key-id", "invalid"}},
		{name: "noncanonical key", args: []string{"--key-id", strings.ToUpper(testKey)}},
		{name: "zero key", args: []string{"--key-id", "00000000-0000-0000-0000-000000000000"}},
		{name: "noncanonical tenant", args: []string{"--tenant", strings.ToUpper(testTenant)}},
		{name: "empty organization", args: []string{"--organization", ""}},
		{name: "whitespace project", args: []string{"--project", " test-project"}},
		{name: "invalid subject kind", args: []string{"--subject-kind", "device"}},
		{name: "empty subject", args: []string{"--subject-id", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseOptions(append(principalArgs(), test.args...), io.Discard); err == nil {
				t.Fatal("invalid arguments were accepted")
			}
		})
	}
}

func TestParseOptionsRedactsValuesAndPreservesHelp(t *testing.T) {
	const secret = "private-value-that-must-not-be-logged"
	for _, args := range [][]string{
		{"--" + secret},
		{"--rotate=" + secret},
		{"--key-id", secret},
		{secret},
	} {
		var output bytes.Buffer
		_, err := parseOptions(append(principalArgs(), args...), &output)
		if err == nil || strings.Contains(err.Error(), secret) || output.Len() != 0 {
			t.Fatalf("invalid arguments were not safely rejected: %v; output: %q", err, output.String())
		}
	}
	var help bytes.Buffer
	if _, err := parseOptions([]string{"--help"}, &help); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("unexpected help result: %v", err)
	}
	if !strings.Contains(help.String(), "-key-id") || !strings.Contains(help.String(), "-subject-kind") {
		t.Fatalf("help is missing principal credential arguments: %s", help.String())
	}
}

func TestCredentialOperationErrorsDoNotExposeDatabaseValues(t *testing.T) {
	const secret = "postgres://operator:private-password@database/execution"
	for _, err := range []error{
		errors.New(secret),
		fmt.Errorf("%w: %s", store.ErrInvalidInput, secret),
		fmt.Errorf("%w: %s", store.ErrNotFound, secret),
		fmt.Errorf("%w: %s", store.ErrExecutorCredentialExists, secret),
	} {
		redacted := credentialOperationError(err)
		if redacted == nil || strings.Contains(redacted.Error(), secret) {
			t.Fatalf("database failure was not redacted: %v", redacted)
		}
	}
	if err := credentialOperationError(nil); err != nil {
		t.Fatalf("successful revocation returned an error: %v", err)
	}
	if !errors.Is(credentialOperationError(store.ErrExecutorCredentialExists), store.ErrExecutorCredentialExists) {
		t.Fatal("duplicate key guidance was lost")
	}
}
