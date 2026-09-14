// Command environment-key manages executor principal credentials using operator DB authority.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Bg().Error("executor credential operation failed", "error", err)
		os.Exit(1)
	}
}

type commandOptions struct {
	principal   identity.Principal
	keyID       string
	environment string
	rotate      bool
	revoke      bool
}

func parseOptions(args []string, helpOutput io.Writer) (commandOptions, error) {
	var options commandOptions
	flags := flag.NewFlagSet("agents-api-environment-key", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.principal.TenantID, "tenant", "", "execution tenant UUID with an existing project mapping")
	flags.StringVar(&options.principal.OrganizationID, "organization", "", "execution organization ID")
	flags.StringVar(&options.principal.ProjectID, "project", "", "execution project ID")
	flags.StringVar(&options.principal.SubjectKind, "subject-kind", "", "executor principal kind: user or service_account")
	flags.StringVar(&options.principal.SubjectID, "subject-id", "", "executor principal subject ID")
	flags.StringVar(&options.keyID, "key-id", "", "canonical nonzero executor key UUID")
	flags.StringVar(&options.environment, "environment", "", "optional existing Environment UUID restriction for issuance only")
	flags.BoolVar(&options.rotate, "rotate", false, "explicitly replace the existing credential, including a revoked credential")
	flags.BoolVar(&options.revoke, "revoke", false, "revoke the existing credential without issuing a secret")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(helpOutput)
			flags.PrintDefaults()
			return commandOptions{}, flag.ErrHelp
		}
		return commandOptions{}, errors.New("invalid executor credential arguments; use --help")
	}
	if flags.NArg() != 0 || (options.rotate && options.revoke) {
		return commandOptions{}, errors.New("positional arguments are not accepted; --rotate and --revoke are mutually exclusive")
	}
	if options.principal.TenantID == "" || options.principal.OrganizationID == "" ||
		options.principal.ProjectID == "" || options.principal.SubjectKind == "" ||
		options.principal.SubjectID == "" || options.keyID == "" {
		return commandOptions{}, errors.New("--tenant, --organization, --project, --subject-kind, --subject-id and --key-id are required")
	}
	if err := options.principal.Validate(); err != nil {
		return commandOptions{}, err
	}
	if !canonicalUUID(options.keyID) {
		return commandOptions{}, errors.New("--key-id must be a canonical nonzero UUID")
	}
	environmentSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "environment" {
			environmentSet = true
		}
	})
	if environmentSet && (options.rotate || options.revoke) {
		return commandOptions{}, errors.New("--environment is only accepted for issuance; rotation and revocation retain the stored restriction")
	}
	if environmentSet && !canonicalUUID(options.environment) {
		return commandOptions{}, errors.New("--environment must be a canonical nonzero UUID")
	}
	return options, nil
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func run() error {
	options, err := parseOptions(os.Args[1:], os.Stderr)
	if err != nil {
		return err
	}
	dsn := os.Getenv("AGENTS_API_DATABASE_URL")
	if dsn == "" {
		return errors.New("AGENTS_API_DATABASE_URL must point to a dedicated execution database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("invalid execution database configuration")
	}
	defer pool.Close()
	s := store.New(pool)
	if options.revoke {
		return credentialOperationError(s.RevokeExecutorCredential(ctx, options.principal, options.keyID))
	}
	var credential store.IssuedExecutorCredential
	if options.rotate {
		credential, err = s.RotateExecutorCredential(ctx, options.principal, options.keyID)
	} else {
		credential, err = s.IssueExecutorCredential(ctx, options.principal, options.keyID, options.environment)
	}
	if err != nil {
		return credentialOperationError(err)
	}
	// Operators redirect stdout to a mode-0600 file under ~/.parsar; no read-back operation exists.
	if err := json.NewEncoder(os.Stdout).Encode(credential); err != nil {
		return errors.New("could not write issued executor credential; rotate explicitly to replace it")
	}
	return nil
}

func credentialOperationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrExecutorCredentialExists):
		return store.ErrExecutorCredentialExists
	case errors.Is(err, store.ErrNotFound):
		return errors.New("executor principal project mapping or authorized credential target not found")
	case errors.Is(err, store.ErrInvalidInput):
		return errors.New("invalid executor credential identity or target")
	default:
		return errors.New("executor credential database operation failed")
	}
}
