// Package main hosts the Parsar HTTP server binary.
//
// The block below is a swaggo/swag general-info annotation. `make openapi`
// invokes `swag init -g cmd/server/main.go` which reads it to synthesize
// the top of docs/openapi/openapi.gen.yaml; each operation's body comes
// from the @Router-annotated handler blocks elsewhere in the tree.
//
//	@title            Parsar API
//	@version          0.1.0
//	@description      Parsar harness API. Optional Feishu SSO and event delivery
//	@description      are enabled only when the corresponding PARSAR_FEISHU_* values are configured.
//	@license.name     Apache 2.0
//	@license.url      https://www.apache.org/licenses/LICENSE-2.0.html
//	@BasePath         /
//	@schemes          http https
//	@securityDefinitions.apikey  CookieAuth
//	@in                          cookie
//	@name                        parsar_session
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	agentdaemonbinding "github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	agentdaemongateway "github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/api"
	imhistoryapi "github.com/MiniMax-AI-Dev/parsar/server/internal/api/imhistoryapi"
	runtimeapi "github.com/MiniMax-AI-Dev/parsar/server/internal/api/runtime"
	specmemapi "github.com/MiniMax-AI-Dev/parsar/server/internal/api/specmem"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
	authgithub "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/github"
	authoidc "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/oidc"
	authpassword "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/password"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/bootstrap"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/config"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	connagentdaemon "github.com/MiniMax-AI-Dev/parsar/server/internal/connector/agentdaemon"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/dev"
	gatewaypkg "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	discordchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/discord"
	slackchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/slack"
	teamschannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/teams"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/imhistory"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/inbound"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/inbound/discordrunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/inbound/slackrunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/inbound/teamsrunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/inflight"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/otlp"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runstream"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runtime/scheduler"
	runtimesweeper "github.com/MiniMax-AI-Dev/parsar/server/internal/runtime/sweeper"
	e2bsandbox "github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/specmemory"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/oss"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
)

type feishuStartupMode string

const (
	feishuStartupModeDisabled feishuStartupMode = "disabled"
	feishuStartupModeMock     feishuStartupMode = "mock"
	feishuStartupModeProd     feishuStartupMode = "prod"
)

type feishuStartupDecision struct {
	Mode                    feishuStartupMode
	RegisterOAuthHandlers   bool
	RegisterWebhookSecurity bool
}

func main() {
	// Install the logger before any other subsystem boots so the
	// config-load error path below flows through the JSON handler.
	log.Init(log.ConfigFromEnv())

	// PARSAR_CONFIG_FILE must be absolute or start with ~/ — the
	// config package does NOT touch CWD.
	loadResult, cfgErr := config.LoadFromOS()
	if cfgErr != nil {
		log.Bg().Error("parsar config invalid", "error", cfgErr)
		os.Exit(1)
	}
	cfg := loadResult.Config
	log.Bg().Info("parsar config loaded",
		"source", loadResult.Source,
		"file", loadResult.FilePath,
		"profile", cfg.Profile(),
		"addr", cfg.Server.Addr,
		"data_dir", cfg.Server.DataDir,
		"dev_auth_enabled", cfg.Auth.DevAuth,
		"cookie_secure", cfg.Auth.Cookie.Secure,
		"sandbox_runner", cfg.Sandbox.Runner,
		"platform_admin_count", len(cfg.Auth.PlatformAdminUserIDs),
	)

	// Loopback PublicURL is the sole remaining dev trigger here (mock /
	// dev-auth already excluded), so secure-cookie and master-key checks
	// get relaxed purely on the URL. The listen addr is independent of
	// PublicURL, so that does NOT keep the service off the network — warn
	// loudly so fronting it with a public proxy isn't a silent footgun.
	if cfg.Profile() == config.ProfileDev && !cfg.Auth.DevAuth && !cfg.Gateway.Feishu.Mock {
		log.Bg().Warn("dev profile active via loopback PARSAR_PUBLIC_URL — secure-cookie and master-key checks are relaxed; do NOT expose this behind a public reverse proxy. Set PARSAR_PUBLIC_URL to your real https:// URL for production.",
			"public_url", cfg.Server.PublicURL,
			"addr", cfg.Server.Addr)
	}

	auth.SetPlatformAdminIDs(cfg.Auth.PlatformAdminUserIDs)

	// envLookup returns YAML+env merged values for keys the config
	// models, and falls through to os.Getenv for keys not folded into
	// typed config (e.g. PARSAR_E2B_*).
	envLookup := cfg.AsEnvLookup(os.Getenv)
	addr := cfg.Server.Addr

	// Dev convenience: when PARSAR_MASTER_KEY isn't set, fall back to
	// a stable dev-only constant that the admin web UI also hardcodes
	// (apps/web/src/lib/api-models.ts DEV_MASTER_KEY). Gated on
	// Profile()==ProfileDev — production fails Validate() upstream.
	const devMasterKeyDefault = "parsar-dev-master-key-2026"
	if cfg.Secret.MasterKey == "" {
		if cfg.Profile() == config.ProfileDev {
			cfg.Secret.MasterKey = devMasterKeyDefault
			if err := os.Setenv(config.EnvMasterKey, devMasterKeyDefault); err != nil {
				log.Bg().Warn("failed to seed dev PARSAR_MASTER_KEY default; secrets will not work",
					"error", err)
			} else {
				// Rebuild envLookup so downstream sub-packages see the
				// dev master key through the merged closure.
				envLookup = cfg.AsEnvLookup(os.Getenv)
				log.Bg().Warn("PARSAR_MASTER_KEY not set — using dev default; do NOT use this default in production",
					"length", len(devMasterKeyDefault))
			}
		}
	}

	// Mirror the master-key fallback for the OTLP receiver Bearer
	// signing key: production failed Validate() upstream when
	// audit.otlp.enabled=true and the key was blank, so this
	// substitution can only fire in dev.
	const devOTLPSigningKeyDefault = "parsar-dev-otlp-signing-key-2026" // #nosec G101
	if cfg.Audit.OTLP.SigningKey == "" && cfg.Profile() == config.ProfileDev {
		cfg.Audit.OTLP.SigningKey = devOTLPSigningKeyDefault
		if err := os.Setenv(config.EnvAuditOTLPSigningKey, devOTLPSigningKeyDefault); err != nil {
			log.Bg().Warn("failed to seed dev PARSAR_AUDIT_OTLP_SIGNING_KEY default; OTLP receiver will fail to start",
				"error", err)
		} else {
			envLookup = cfg.AsEnvLookup(os.Getenv)
			log.Bg().Warn("PARSAR_AUDIT_OTLP_SIGNING_KEY not set — using dev default; do NOT use this default in production",
				"length", len(devOTLPSigningKeyDefault))
		}
	}

	if cfg.Gateway.Feishu.RedirectURI == "" {
		cfg.Gateway.Feishu.RedirectURI = cfg.BuildPublicURL("/api/v1/auth/feishu/callback")
		envLookup = cfg.AsEnvLookup(os.Getenv)
	}
	if strings.TrimSpace(envLookup(authgithub.EnvRedirectURI)) == "" && strings.TrimSpace(envLookup(authgithub.EnvClientID)) != "" {
		if err := os.Setenv(authgithub.EnvRedirectURI, cfg.BuildPublicURL("/api/v1/connections/github/callback")); err != nil {
			log.Bg().Warn("failed to seed GitHub OAuth redirect URI", "error", err)
		} else {
			envLookup = cfg.AsEnvLookup(os.Getenv)
		}
	}

	feishuDecision := decideFeishuStartup(envLookup)
	switch feishuDecision.Mode {
	case feishuStartupModeMock:
		log.Bg().Warn("DEV MODE: PARSAR_FEISHU_MOCK=true — using mock Feishu auth and skipping webhook verification")
	case feishuStartupModeProd:
		log.Bg().Info("feishu prod mode configured",
			"oauth_handlers", feishuDecision.RegisterOAuthHandlers,
			"webhook_security", feishuDecision.RegisterWebhookSecurity)
	default:
		log.Bg().Info("feishu auth/event routes disabled; email/password setup remains available")
	}

	pool := openPool(cfg.Database.URL)
	dbStore, auditIngester := buildStore(pool, cfg.Audit.OTLP.FanoutEndpoint)

	// Object storage client — backs capability-plugin upload + dispatch.
	// Nil when the operator did not configure OSS env vars; downstream
	// consumers tolerate nil and 503 / log-warn rather than failing boot.
	var ossClient *oss.Client
	if ossCfg := cfg.Storage.OSS; ossCfg.Bucket != "" {
		built, err := oss.New(oss.Config{
			Region:          ossCfg.Region,
			Endpoint:        ossCfg.Endpoint,
			Bucket:          ossCfg.Bucket,
			AccessKeyID:     ossCfg.AccessKeyID,
			AccessKeySecret: ossCfg.AccessKeySecret,
			BaseURL:         ossCfg.BaseURL,
		})
		if err != nil {
			log.Bg().Warn("OSS client construction failed; plugin capability will be unavailable", "error", err)
		} else {
			ossClient = built
			log.Bg().Info("OSS client initialized", "region", ossCfg.Region, "bucket", ossCfg.Bucket)
		}
	} else {
		log.Bg().Info("OSS not configured (storage.oss.bucket empty); plugin capability disabled")
	}

	// Capability blob storage. Default backend is Postgres (zero external
	// infra); operators opt into OSS with PARSAR_BLOB_BACKEND=oss. blobStore
	// is nil when the chosen backend isn't usable (no pool / no OSS) —
	// upload routes then 503 exactly like the old OSS-not-configured path.
	var blobStore blob.Store
	var blobProxy *blob.ProxyHandler
	switch strings.ToLower(strings.TrimSpace(cfg.Storage.BlobBackend)) {
	case "oss":
		if ossClient != nil {
			blobStore = blob.NewOSSStore(ossClient)
			log.Bg().Info("capability blob backend: oss")
		} else {
			log.Bg().Warn("PARSAR_BLOB_BACKEND=oss but OSS is not configured; capability upload disabled")
		}
	default: // "pg" and anything unrecognized fall back to Postgres.
		if pool != nil {
			signer := blob.NewProxySigner(cfg.Secret.MasterKey)
			if !signer.Enabled() {
				log.Bg().Warn("capability blob backend pg: master key empty; proxy tokens cannot be signed (set PARSAR_MASTER_KEY)")
			}
			// baseURL must be daemon-reachable; reuse the public-URL builder
			// (dev fallback http://127.0.0.1:18080; prod requires PublicURL).
			baseURL := strings.TrimSuffix(cfg.BuildPublicURL("/"), "/")
			pgStore := blob.NewPGStore(sqlc.New(pool), signer, baseURL)
			blobStore = pgStore
			blobProxy = blob.NewProxyHandler(pgStore, signer)
			log.Bg().Info("capability blob backend: pg", "base_url", baseURL)
		} else {
			log.Bg().Warn("capability blob backend pg: no database pool; capability upload disabled")
		}
	}

	// Spec/memory service: reused by connectors and HTTP routes so
	// admin writes and runtime writes land on the same audit ingester.
	// Nil dbStore → connectors treat nil SpecMemory as "injection disabled".
	var specmemSvc *specmemory.Service
	if dbStore != nil {
		specmemSvc = specmemory.NewService(dbStore, auditIngester, specmemory.Options{
			Logger: log.Bg(),
		})
	}

	r := chi.NewRouter()
	// Trace middleware first so every downstream handler emits log
	// lines tagged with a stable trace_id.
	r.Use(log.HTTPMiddleware)
	// PG blob proxy: authenticated PUT/GET for capability zips when the
	// Postgres backend is active. Token-authenticated inside the handler;
	// intentionally NOT behind session middleware (the daemon and the
	// browser presigned-PUT both call it with a signed token).
	if blobProxy != nil {
		r.Handle(blob.ProxyPathPrefix+"*", blobProxy)
	}
	// Wrap pool in a local var so the Pinger interface ends up nil
	// when pool is nil — assigning a typed-nil *pgxpool.Pool would
	// otherwise produce a non-nil interface holding a nil pointer and
	// crash /readyz on every poll.
	var dbPinger api.Pinger
	if pool != nil {
		dbPinger = pool
	}
	api.RegisterHealthRoutes(r, api.HealthDeps{DB: dbPinger})
	api.RegisterDocsRoutes(r, api.DocsOptions{
		SpecPath: api.ResolveOpenAPISpecPath(),
		Title:    "Parsar API",
		Logger:   func(format string, args ...any) { log.Bg().Warn(fmt.Sprintf(format, args...)) },
	})
	api.RegisterParsarDaemonInstallRoute(r, api.ParsarDaemonInstallConfig{
		Repo: strings.TrimSpace(os.Getenv("PARSAR_DAEMON_REPO")),
	})
	api.RegisterParsarDaemonDownloadRoute(r, api.ParsarDaemonDownloadConfig{
		BinaryDir: strings.TrimSpace(os.Getenv("PARSAR_DAEMON_BINARY_DIR")),
	})

	// Bootstrap (first-owner provisioning) and email/password login both
	// mount OUTSIDE the auth middleware because no user exists yet to
	// authenticate. The bootstrap gate is `count(active owners) == 0`
	// enforced under a Postgres advisory lock in the tx; the login
	// endpoint is public but rate-limited.
	if pool != nil && dbStore != nil {
		bootstrapSessionStore := auth.NewPostgresSessionStore(sqlc.New(pool))
		cookieSecure := cfg.Auth.Cookie.Secure

		bootstrapSvc := bootstrap.NewService(dbStore,
			bootstrap.WithPublicURL(cfg.Server.PublicURL))
		bootstrap.RegisterRoutes(r, bootstrapSvc,
			func() bool { return cfg.Auth.DevAuth },
			bootstrapSessionStore, cookieSecure)
		log.Bg().Info("bootstrap endpoint",
			"dev_auth", cfg.Auth.DevAuth,
			"cookie_secure", cookieSecure,
		)

		loginH := authpassword.NewLoginHandler(sqlc.New(pool), bootstrapSessionStore, cookieSecure, nil)
		r.Group(func(r chi.Router) {
			// 10 login attempts/minute/IP is generous for humans and
			// still throttles credential-stuffing. KeyByIP keys off
			// r.RemoteAddr; behind a reverse proxy, operators should
			// install chi's ClientIPFrom* middleware ahead of the
			// router so RemoteAddr is the real client.
			r.Use(httprate.LimitBy(10, time.Minute, httprate.KeyByIP))
			r.Post("/api/v1/auth/login", loginH.Login)
		})
		r.Post("/api/v1/auth/logout", loginH.Logout)
	}

	// Hoist these so downstream route registration can see them; nil
	// means agent_daemon gateway is not exposed.
	var agentDaemonHandler *agentdaemongateway.Handler
	var runtimeStatusProber dev.SandboxLivenessProber
	managedSandboxProviderWired := false
	// serverRootCtx is forward-declared so we can pass it to
	// dev.WithDispatchContext before the dev router is built.
	serverRootCtx, serverRootStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// Shared runstream broker: the dev router publishes events into it
	// from dispatchConversationRun; SSE /stream subscribers drain it.
	// A freshly-created agent_daemon run auto-started by the store
	// must publish into the same broker the UI /stream is subscribing
	// to, otherwise events land in a per-call broker and the UI hangs.
	runStreamBroker := runstream.NewBroker(runstream.DefaultBufferSize)
	oidcPublicURL := strings.TrimRight(cfg.BuildPublicURL("/"), "/")
	oidcClients, oidcStatuses, oidcErr := authoidc.NewClientsFromEnv(authoidc.EnvFunc(envLookup), oidcPublicURL)
	if oidcErr != nil {
		log.Bg().Warn("OIDC auth providers disabled", "error", oidcErr)
	}
	opts := []dev.RouterOption{
		dev.WithDispatchContext(serverRootCtx),
		dev.WithRunStreamBroker(runStreamBroker),
		dev.WithAuthProviders(buildAuthProviderRegistry(envLookup, cfg, feishuDecision, oidcStatuses)),
	}
	if shouldRegisterFeishuAppProvisioning(cfg) {
		if regClient, err := gatewaypkg.NewFeishuAppRegistrationClient(gatewaypkg.FeishuAppRegistrationClientOptions{
			AccountsBaseURL: strings.TrimSpace(envLookup(feishu.EnvAuthorizeBase)),
			OpenBaseURL:     strings.TrimSpace(envLookup(feishu.EnvAPIBase)),
		}); err != nil {
			log.Bg().Warn("feishu app-registration client not registered", "error", err)
		} else {
			opts = append(opts, dev.WithFeishuAppRegistration(regClient, strings.TrimSpace(envLookup(feishu.EnvAPIBase))))
			log.Bg().Info("feishu app-registration provisioning registered")
		}
	}
	if feishuDecision.RegisterWebhookSecurity {
		opts = append(opts, dev.WithFeishuWebhookSecurity(
			feishuDecision.Mode == feishuStartupModeMock,
			strings.TrimSpace(envLookup(feishu.EnvVerificationToken)),
			strings.TrimSpace(envLookup(feishu.EnvEncryptKey)),
		))
	}
	// connectorReg is hoisted so the Feishu inbound + outbound builders
	// below can wrap the agent_daemon AgentConnector as a
	// PermissionRouter. Stays empty when dbStore is nil.
	connectorReg := connector.NewRegistry()
	// Shared signer for the auto-mounted fetch_chat_history tool. Hoisted to
	// function scope so both the agent_daemon connector (mints per-conversation
	// tokens) and the internal history endpoint (verifies them) share one
	// master-key-derived secret. Nil ⇒ empty master key ⇒ both are skipped.
	var imHistorySigner *imhistoryapi.Signer
	if secret := imhistoryapi.DeriveSecret(cfg.Secret.MasterKey); secret != "" {
		if s, err := imhistoryapi.NewSigner(secret); err != nil {
			log.Bg().Error("im history signer init failed", "error", err)
		} else {
			imHistorySigner = s
		}
	}
	if dbStore != nil {
		// Shared connector registry: every AgentConnector registers
		// under its Type() so dev router / permission decide / run
		// cancel can dispatch via lookup rather than a hard-coded switch.
		opts = append(opts, dev.WithConnectorRegistry(connectorReg))

		// SweepOrphanedSandboxBindings is intentionally kept in the
		// store layer for a future heartbeat-driven reaper to reuse.
		// The startup sweep was written for the legacy Acquire flow
		// whose envd token lived only in the previous process; under
		// agent_daemon the sandbox outlives the server process and the
		// daemon reconnects via persisted runner_credential.

		// PublicWSURL derives from cfg.Server.PublicURL by swapping the
		// scheme (http→ws, https→wss). Dev falls back to 127.0.0.1:18080;
		// prod boots without public_url fail config validate.go.
		agentDaemonBinder := agentdaemonbinding.NewPgBinder(pool, func(format string, args ...any) {
			log.Bg().Warn("agentdaemon binder", "msg", fmt.Sprintf(format, args...))
		})
		agentDaemonRegistry := agentdaemongateway.NewRegistry()
		agentDaemonAuth := agentdaemongateway.NewAuthenticator(dbStore)
		publicWSURL := resolveAgentDaemonPublicWSURL(envLookup, cfg)
		agentDaemonPodID := resolveAgentDaemonOwnerPodID(envLookup)
		agentDaemonOwnerURL, err := resolveAgentDaemonOwnerURL(envLookup, cfg)
		if err != nil {
			log.Bg().Error("agent_daemon owner URL not resolvable; aborting startup", "error", err.Error())
			os.Exit(1)
		}
		agentDaemonInternalToken := resolveAgentDaemonInternalToken(envLookup, cfg.Secret.MasterKey)
		agentDaemonHandler = agentdaemongateway.NewHandler(agentdaemongateway.HandlerConfig{
			Authenticator: agentDaemonAuth,
			Registry:      agentDaemonRegistry,
			Heartbeat:     dbStore,
			PublicWSURL:   publicWSURL,
			OwnerStore:    dbStore,
			OwnerPodID:    agentDaemonPodID,
			OwnerURL:      agentDaemonOwnerURL,
			Log: func(format string, args ...any) {
				log.Bg().Info("agentdaemon gateway", "msg", fmt.Sprintf(format, args...))
			},
		})
		agentDaemonSandbox := buildAgentDaemonSandboxProvider(envLookup, cfg, dbStore, agentDaemonRegistry, agentDaemonBinder, agentDaemonPodID)
		agentDaemonRemote := connagentdaemon.HTTPRemoteStreamer{Token: agentDaemonInternalToken}
		agentDaemonCfg := connagentdaemon.Config{
			Registry:          agentDaemonRegistry,
			Binder:            agentDaemonBinder,
			Sandbox:           agentDaemonSandbox,
			OwnerResolver:     dbStore,
			OwnerPodID:        agentDaemonPodID,
			Remote:            agentDaemonRemote,
			RemoteSubmit:      agentDaemonRemote,
			SubmitSlots:       dbStore,
			ModelResolver:     dbStore,
			ExecutionRecorder: dbStore,
			RunStatusReader:   dbStore,
			Capabilities:      dbStore,
			MasterKey:         cfg.Secret.MasterKey,
			// Auto-mounted fetch_chat_history tool: the endpoint URL the
			// sandbox calls back into, plus the per-conversation token signer.
			// Nil signer (empty master key) disables the injection.
			IMHistoryEndpoint: cfg.BuildPublicURL("/internal/im/history"),
			IMHistoryTokenSigner: func() func(string) string {
				if imHistorySigner == nil {
					return nil
				}
				return imHistorySigner.Token
			}(),
			// Spec/memory SessionStart injection. override_system_prompt
			// still wins; nil is a no-op.
			SpecMemory: specmemSvc,
			// Soft-degrade nudges: without this the Feishu outbound
			// driver receives no notice when an MCP is soft-degraded
			// and the credential-form recovery loop never fires.
			SystemMessages: dbStore,
			// Sandbox binding reader lets the connector turn the
			// generic "Agent not bound to Runtime" hint into a precise
			// spawning / failed / never-attempted message.
			SandboxBindingReader: dbStore,
			Log:                  log.Bg(),
		}
		// The daemon only needs a short-lived GET URL per capability ref.
		// blobDownloadAdapter bridges blob.Store onto the daemon's existing
		// OSSPresigner surface so the agentdaemon package is backend-agnostic
		// without any churn. Guarded so a nil store stays a nil interface and
		// the connector's `c.oss == nil` skip still fires.
		if blobStore != nil {
			agentDaemonCfg.OSS = blobDownloadAdapter{store: blobStore}
		}
		agentDaemonConn := connagentdaemon.New(agentDaemonCfg)
		connagentdaemon.RegisterInternalRoutes(r, agentDaemonConn, agentDaemonInternalToken)
		if regErr := connectorReg.Register(agentDaemonConn); regErr != nil {
			log.Bg().Warn("agent_daemon connector registry registration failed; agent_daemon dispatch will 503",
				"error", regErr,
				"type", agentDaemonConn.Type())
		} else {
			log.Bg().Info("agent_daemon AgentConnector registered",
				"type", agentDaemonConn.Type(),
				"capabilities", agentDaemonConn.Capabilities().String(),
				"ws_url", publicWSURL,
			)
		}

		// Wire the agent_daemon sandbox provider into the dev router
		// so createAgent can eager-acquire and the /sandbox/acquire
		// endpoint works. nil provider is safe — both paths degrade
		// to lazy-create or 503 respectively.
		if agentDaemonSandbox != nil {
			managedSandboxProviderWired = true
			runtimeStatusProber = configuredSandboxProber{}
			opts = append(opts, dev.WithAgentDaemonSandbox(agentDaemonSandbox))
			opts = append(opts, dev.WithSandboxLifecycle(dbStore, agentDaemonSandbox))

			// Reap ticker: evict idle agent_daemon sandboxes so they
			// don't leak E2B resources. 5-minute interval mirrors the
			// opencode idle sweeper cadence.
			go func() {
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-serverRootCtx.Done():
						return
					case <-ticker.C:
						reapCtx, reapCancel := context.WithTimeout(context.Background(), 30*time.Second)
						if n, reapErr := agentDaemonSandbox.Reap(reapCtx); reapErr != nil {
							log.Bg().Warn("agent_daemon sandbox reap failed", "err", reapErr)
						} else if n > 0 {
							log.Bg().Info("agent_daemon sandbox reap", "evicted", n)
						}
						reapCancel()
					}
				}
			}()
		}

		// Abandoned credential-form slot sweep (1h TTL). The slot lives
		// inside conversations.metadata.gateway_inflight rather than a
		// side table; sweep removes the expired subkey. Errors are
		// logged + swallowed — best-effort, a missed cycle clears at
		// the next pass.
		if dbStore != nil {
			go func() {
				ticker := time.NewTicker(10 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-serverRootCtx.Done():
						return
					case <-ticker.C:
						sweepCtx, sweepCancel := context.WithTimeout(context.Background(), 15*time.Second)
						if n, err := dbStore.SweepExpiredPendingCredentialFormSlots(sweepCtx, time.Now().UTC()); err != nil {
							log.Bg().Warn("feishu credential form sweep failed", "err", err)
						} else if n > 0 {
							log.Bg().Info("feishu credential form sweep", "cleared", n)
						}
						if stale, err := dbStore.CountStalePendingCredentialFormSlots(sweepCtx, time.Now().UTC().Add(-time.Hour)); err != nil {
							log.Bg().Warn("feishu credential form stale count failed", "err", err)
						} else if stale > 0 {
							// Non-zero past now-1h means the 10-min
							// sweep is not keeping up. Log at WARN
							// for operator visibility.
							log.Bg().Warn("feishu credential form stale slots present",
								"stale_count", stale,
								"hint", "sweep cron may be stalled or running behind",
							)
						}
						sweepCancel()
					}
				}
			}()
		}

		// Streaming-dispatch hook: auto-start agent_daemon AgentRuns
		// the moment SendUserMessageToConversation /
		// CreateInboundIMMessage / agent-to-agent commit. Without
		// this, a freshly created agent_daemon run sits at
		// status=queued forever. The adapter shares runStreamBroker
		// + connectorReg with the dev router so events from the
		// auto-started goroutine reach the same /stream subscribers.
		dbStore.SetStreamingDispatcher(streamingDispatcherAdapter{
			runtimeStore: dbStore,
			deps: dev.StreamingDispatchDeps{
				Broker:            runStreamBroker,
				ConnectorRegistry: connectorReg,
				DispatchCtx:       serverRootCtx,
			},
			failRun: func(ctx context.Context, runID, source, reason string) {
				if err := dbStore.FailAgentRun(ctx, store.FailAgentRunInput{RunID: runID, Source: source, Reason: reason}); err != nil {
					log.Bg().Error("streaming dispatcher: FailAgentRun after auto-start failure also failed",
						"run_id", runID, "source", source, "error", err)
				}
			},
		})
		log.Bg().Info("streaming dispatch hook wired")
	}

	// Runtime status banner: ConfiguredByOps marks "operator set
	// PARSAR_OPENCODE_RUNNER explicitly" and is retained only for
	// the legacy admin badge query; it no longer drives runtime
	// selection.
	runtimeProfile := resolveRuntimeProfile(envLookup, managedSandboxProviderWired)
	runtimeStatusDeps := dev.RuntimeStatusDeps{
		SettingsStore:   dbStore,
		SandboxProber:   runtimeStatusProber,
		Profile:         runtimeProfile,
		ConfiguredByOps: strings.TrimSpace(envLookup("PARSAR_OPENCODE_RUNNER")) != "",
		SandboxImage:    configuredDockerSandboxImage(envLookup),
	}
	log.Bg().Info("runtime status profile configured",
		"profile", runtimeProfile,
		"managed_sandbox_provider", managedSandboxProviderWired)
	opts = append(opts, dev.WithRuntimeStatus(runtimeStatusDeps))
	if blobStore != nil {
		opts = append(opts, dev.WithBlobStore(blobStore))
	}

	// Wire the audit ingester for dev handlers that emit directly.
	// Nil-safe: nil store treats it as "audit not wired" (best-effort).
	if auditIngester != nil {
		opts = append(opts, dev.WithAuditIngester(auditIngester))
	}

	// Session middleware so protected /dev/* routes require a resolved
	// user from the HttpOnly cookie. The explicit dev header shim is
	// available only when PARSAR_DEV_AUTH=true.
	if pool != nil && dbStore != nil {
		sessionStore := auth.NewPostgresSessionStore(sqlc.New(pool))
		// Wire auth middleware with the merged Config view of dev_auth
		// (YAML + env). Without .WithDevAuth, NewMiddleware reads
		// PARSAR_DEV_AUTH from os.Getenv directly, so an operator
		// who sets `auth.dev_auth: true` only in YAML would see
		// Profile()==dev but the dev shim would silently stay disabled.
		opts = append(opts, dev.WithAuthMiddleware(
			auth.NewMiddleware(sessionStore).WithDevAuth(cfg.Auth.DevAuth),
		))
		log.Bg().Info("auth middleware mode",
			"dev_auth_enabled", cfg.Auth.DevAuth,
		)

		githubClient, githubErr := authgithub.NewClientFromEnv(envLookup)
		if githubErr != nil {
			log.Bg().Info("github OAuth connection not configured", "error", githubErr)
		}
		githubRedirect := strings.TrimSpace(cfg.Gateway.Feishu.LoginRedirectURL)
		opts = append(opts, dev.WithGitHubConnectionHandlers(dev.GitHubConnectionDeps{
			Client:       githubClient,
			RedirectURL:  githubRedirect,
			CookieSecure: cfg.Auth.Cookie.Secure,
		}))

		if feishuDecision.RegisterOAuthHandlers || len(oidcClients) > 0 {
			loginRedirect := strings.TrimSpace(cfg.Gateway.Feishu.LoginRedirectURL)
			cookieSecure := cfg.Auth.Cookie.Secure
			deps := dev.OAuthHandlerDeps{
				OIDC:             oidcClients,
				Sessions:         sessionStore,
				Store:            dbStore,
				CookieSecure:     cookieSecure,
				LoginRedirectURL: loginRedirect,
			}
			feishuClient, err := feishu.NewClientFromEnv(envLookup)
			if feishuDecision.RegisterOAuthHandlers && err != nil {
				log.Bg().Warn("feishu OIDC client not registered", "error", err)
			}
			if err == nil {
				deps.Feishu = feishuClient
			}
			opts = append(opts, dev.WithOAuthHandlers(deps))
			if deps.Feishu != nil {
				log.Bg().Info("feishu OIDC handlers registered",
					"mode", feishuDecision.Mode, "mock", feishuClient.IsMock(), "cookie_secure", cookieSecure,
					"login_redirect", loginRedirect)
			}
			if len(oidcClients) > 0 {
				log.Bg().Info("OIDC auth handlers registered",
					"providers", len(oidcClients), "cookie_secure", cookieSecure,
					"login_redirect", loginRedirect)
			}
		}
	}

	var runtimeStore dev.RuntimeStore
	if dbStore != nil {
		runtimeStore = dbStore
	}
	// Wire the visibility=workspace rejection card's "Join request" URL
	// builder. Only when PublicURL is configured — nil builder leaves
	// the rejection card link-free (falls back to "Please contact the administrator above to join").
	if strings.TrimSpace(cfg.Server.PublicURL) != "" {
		opts = append(opts, dev.WithFeishuJoinURLBuilder(func(workspaceID string) string {
			return cfg.BuildPublicURL("/join-workspace?id=" + workspaceID + "&from=feishu")
		}))
	}
	if pool != nil && dbStore != nil {
		publicURL := strings.TrimSpace(cfg.Server.PublicURL)
		if publicURL == "" {
			publicURL = "http://localhost" + cfg.Server.Addr
		}
		sessionStore := auth.NewPostgresSessionStore(sqlc.New(pool))
		opts = append(opts, dev.WithInvite(sessionStore, cfg.Auth.Cookie.Secure, publicURL))
		log.Bg().Info("invite-link system enabled")
	}
	dev.RegisterRoutesWithStore(r, runtimeStore, opts...)

	// imHistoryResolver is bound to the outbound worker once it is built
	// (later in boot than routes are registered); until then the internal
	// history endpoint reports an empty page rather than failing.
	imHistoryResolver := &imhistoryapi.LateResolver{}

	// Runtime lifecycle API: admin tree behind session middleware,
	// runtime credential tree open at chi with Bearer auth enforced
	// inside the package. Wired AFTER dev.RegisterRoutesWithStore so
	// chi's NotFound fallback still fires last.
	if pool != nil && dbStore != nil {
		runtimeDeps := runtimeapi.Deps{
			Store:              dbStore,
			SharedRuntimeToken: strings.TrimSpace(envLookup("PARSAR_SHARED_RUNTIME_TOKEN")),
		}
		sessionStore := auth.NewPostgresSessionStore(sqlc.New(pool))
		authMw := auth.NewMiddleware(sessionStore).WithDevAuth(cfg.Auth.DevAuth)
		r.Group(func(r chi.Router) {
			r.Use(authMw.Require)
			runtimeapi.RegisterAdminRoutes(r, runtimeDeps)
		})
		runtimeapi.RegisterRunnerRoutes(r, runtimeDeps)
		log.Bg().Info("runtime lifecycle API mounted",
			"admin_paths", "/api/v1/workspaces/{wid}/runtimes",
			"runtime_paths", "/api/v1/runtimes/pair, /api/v1/runtimes/{id}/heartbeat")

		// Spec/memory: admin tree behind session middleware (cookie auth);
		// runtime tree behind RunnerCredential so the in-sandbox tg CLI's
		// Bearer maps to a RuntimeIdentity in ctx. Both trees share the
		// *specmemory.Service so audit events land on the same ingester.
		specmemDeps := specmemapi.Deps{
			Service:    specmemSvc,
			Membership: dbStore,
			Logger:     log.Bg(),
		}
		r.Group(func(r chi.Router) {
			r.Use(authMw.Require)
			specmemapi.RegisterAdminRoutes(r, specmemDeps)
		})
		r.Group(func(r chi.Router) {
			r.Use(auth.RunnerCredential(dbStore, auth.RunnerCredentialOptions{
				Logger: log.Bg(),
			}))
			specmemapi.RegisterRuntimeRoutes(r, specmemDeps)
		})
		log.Bg().Info("spec/memory API mounted",
			"admin_paths", "/api/v1/workspaces/{wid}/spec/..., /api/v1/memories",
			"runtime_paths", "/api/v1/agent-runtime/...")

		// agent_daemon WS / bootstrap routes. Nil handler when connector
		// wiring above failed; skipping the mount keeps an otherwise-
		// healthy server from advertising an endpoint that would panic.
		if agentDaemonHandler != nil {
			agentdaemongateway.RegisterRoutes(r, agentDaemonHandler)
			log.Bg().Info("agent_daemon gateway mounted",
				"ws_path", "/agent-daemon/ws",
				"bootstrap_path", "/agent-daemon/bootstrap",
				"status_path", "/agent-daemon/device-status")
		}

		// Internal on-demand chat-history endpoint the auto-mounted
		// fetch_chat_history MCP tool calls back into. Reuses imHistorySigner
		// (built above for the connector's token minting) so both sides share
		// one master-key-derived secret. Nil signer ⇒ empty master key ⇒
		// endpoint skipped (the tool injection is likewise skipped). The Gate
		// supplies the never-fail guarantees (serialize + retry + cache).
		if imHistorySigner != nil {
			imHistoryGate := imhistory.New(imhistory.Options{})
			imHistoryGate.SetLogger(log.With("component", "imhistory"))
			imhistoryapi.RegisterRoutes(r, imhistoryapi.Deps{
				Store:    dbStore,
				Resolver: imHistoryResolver,
				Signer:   imHistorySigner,
				Gate:     imHistoryGate,
			})
			log.Bg().Info("im history endpoint mounted", "path", "/internal/im/history")
		}
	}

	// Mount SPA static-asset handler AFTER every API route so chi's
	// NotFound only fires for paths the API does not own. No-op when
	// PARSAR_WEB_DIST is unset (the `make dev` profile) or when the
	// directory is missing/lacks index.html. The production image
	// bakes the Vite build at /app/web/dist.
	webDir := strings.TrimSpace(envLookup("PARSAR_WEB_DIST"))
	mounted := api.MountStaticAssets(r, api.StaticAssetsOptions{
		Dir: webDir,
		Logger: func(format string, args ...any) {
			log.Bg().Warn(fmt.Sprintf(format, args...),
				"subsystem", "static_assets")
		},
	})
	if mounted {
		log.Bg().Info("SPA static assets mounted",
			"dir", webDir)
	} else if webDir != "" {
		// Operator set PARSAR_WEB_DIST but mount failed (warning
		// already emitted above). This INFO confirms the dev path
		// is active so operators can tell apart "intentional dev"
		// from "broken prod config".
		log.Bg().Info("SPA static assets NOT mounted (see warning above)")
	}

	// Drain the audit ingester and OpenCode LocalPool / opencode
	// subprocesses on SIGINT/SIGTERM so children don't reparent to
	// init and audit events still in buffer get flushed before exit.
	// K8s callers must size terminationGracePeriodSeconds accordingly.
	ctx, stop := serverRootCtx, serverRootStop
	defer stop()

	if auditIngester != nil {
		auditIngester.Start(ctx)
	}

	// Optional embedded OTLP/HTTP receiver so sandbox sidecars / OTel-
	// SDK producers can ship tool-lifecycle events into the same
	// audit_records pipeline. Disabled by default; opt-in via
	// PARSAR_AUDIT_OTLP_ENABLED.
	var otlpReceiver *otlp.Receiver
	if auditIngester != nil && cfg.Audit.OTLP.Enabled {
		signer, err := otlp.NewSigner(cfg.Audit.OTLP.SigningKey, otlp.SignerOptions{})
		if err != nil {
			log.Bg().Error("otlp signer init failed; receiver cannot start without a signing key",
				"error", err)
			os.Exit(1)
		}
		rec, err := otlp.NewReceiver(otlp.Config{
			Addr:     cfg.Audit.OTLP.Addr,
			Ingester: auditIngester,
			Signer:   signer,
		})
		if err != nil {
			log.Bg().Error("otlp receiver init failed", "error", err)
			os.Exit(1)
		}
		if err := rec.Start(ctx); err != nil {
			log.Bg().Error("otlp receiver bind failed",
				"addr", cfg.Audit.OTLP.Addr, "error", err)
			os.Exit(1)
		}
		otlpReceiver = rec
		log.Bg().Info("audit otlp receiver enabled", "addr", rec.Addr())
	}

	// Runtime heartbeat sweeper: demote runtimes whose heartbeat has
	// gone stale. Prevents Agent Daemon devices from staying online
	// forever after a laptop sleep, process crash, or broken network.
	if dbStore != nil {
		if sw := buildRuntimeHeartbeatSweeper(envLookup, dbStore); sw != nil {
			go func() {
				if err := sw.Run(ctx); err != nil {
					log.Bg().Error("runtime heartbeat sweeper exited with error", "error", err)
				}
			}()
		}
	}

	// Scheduled task scheduler: each cron fire dispatches an independent
	// agent run (own session) into the task's container conversation.
	if dbStore != nil {
		if sc := buildScheduler(envLookup, dbStore); sc != nil {
			go func() {
				if err := sc.Run(ctx); err != nil {
					log.Bg().Error("scheduled task scheduler exited with error", "error", err)
				}
			}()
		}
	}

	// OSS lazy mode (PARSAR_FEISHU_OSS_SHARE_OAUTH_APP=true): collapses
	// OAuth platform App and Feishu Bot onto a single App ID — only safe
	// with ≤ 1 active Feishu-bot Agent. Refuse to start if more exist.
	if dbStore != nil && truthy(os.Getenv("PARSAR_FEISHU_OSS_SHARE_OAUTH_APP")) {
		if n, err := dbStore.CountActiveFeishuBotAgents(ctx); err != nil {
			log.Bg().Error("OSS lazy mode: count feishu bot agents failed", "error", err)
			os.Exit(1)
		} else if n > 1 {
			log.Bg().Error("OSS lazy mode (PARSAR_FEISHU_OSS_SHARE_OAUTH_APP=true) is incompatible with the current data — more than one active Agent has a Feishu Bot enabled, but the lazy mode collapses OAuth + Bot onto a single App ID and supports at most one Bot Agent. Either disable the env flag and run separate Bot apps per Agent, or disable all but one Agent's Feishu connector.",
				"active_bot_agents", n)
			os.Exit(1)
		} else {
			log.Bg().Warn("OSS lazy mode enabled — OAuth platform App and Feishu Bot share App ID. Only safe with ≤ 1 active Bot Agent.",
				"active_bot_agents", n)
		}
	}

	// Feishu Bot event-websocket inbound manager. Opt-in via
	// PARSAR_FEISHU_WEBSOCKET=true. QR-provisioned Bots write
	// event_mode=websocket and need this manager to turn Feishu
	// events into Parsar gateway messages.
	if dbStore != nil {
		if manager, err := buildFeishuWebSocketManager(os.Getenv, dbStore, connectorReg, cfg.Server.PublicURL); err != nil {
			log.Bg().Error("feishu websocket inbound manager init failed", "error", err)
			os.Exit(1)
		} else if manager != nil {
			go func() {
				if err := manager.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Bg().Error("feishu websocket inbound manager exited", "error", err)
				}
			}()
		}
	}

	// Slack Socket Mode inbound runner. Opt-in via PARSAR_SLACK_SOCKET=true.
	// Feeds the same neutral router.HandleInbound pipeline as Feishu; inbound
	// only (agent async answers don't return to Slack until a neutral outbound
	// worker lands). Shares the router store; no DDL.
	if dbStore != nil {
		if runner, err := buildSlackRunner(os.Getenv, dbStore, connectorReg, cfg.Server.PublicURL); err != nil {
			log.Bg().Error("slack socket mode runner init failed", "error", err)
			os.Exit(1)
		} else if runner != nil {
			go func() {
				if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Bg().Error("slack socket mode runner exited", "error", err)
				}
			}()
		}
	}

	// Discord Gateway WebSocket inbound runner. Opt-in via
	// PARSAR_DISCORD_GATEWAY=true. Feeds the same neutral router.HandleInbound
	// pipeline as Feishu/Slack; inbound only. Shares the router store; no DDL.
	// MESSAGE_CONTENT is a privileged gateway intent — enable it in the Discord
	// Developer Portal or message bodies arrive empty.
	if dbStore != nil {
		if runner, err := buildDiscordRunner(os.Getenv, dbStore, connectorReg, cfg.Server.PublicURL); err != nil {
			log.Bg().Error("discord gateway runner init failed", "error", err)
			os.Exit(1)
		} else if runner != nil {
			go func() {
				if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Bg().Error("discord gateway runner exited", "error", err)
				}
			}()
		}
	}

	// Microsoft Teams webhook inbound runner. Opt-in via PARSAR_TEAMS_WEBHOOK=
	// true. Unlike Slack/Discord (socket/gateway Run loops) Teams delivers
	// inbound activities as HTTPS POSTs, so the runner is an http.Handler mounted
	// on the chi router rather than a goroutine. Feeds the same neutral
	// router.HandleInbound pipeline; inbound only. Shares the router store; no DDL.
	if dbStore != nil {
		if runner, err := buildTeamsRunner(os.Getenv, dbStore, connectorReg, cfg.Server.PublicURL); err != nil {
			log.Bg().Error("teams webhook runner init failed", "error", err)
			os.Exit(1)
		} else if runner != nil {
			r.Post("/api/teams/messages", runner.Handler())
			log.Bg().Info("teams webhook runner mounted", "path", "/api/teams/messages")
		}
	}

	// Slack workspace-dimension reconciler. Opt-in via PARSAR_SLACK_CONNECTORS=
	// true. Reads workspace_im_connectors and keeps one Socket Mode runner per
	// workspace|app_id, hot-reloading on token rotation — the DB-driven twin of
	// the env-gated buildSlackRunner above.
	if dbStore != nil {
		if mgr, err := buildSlackInboundManager(os.Getenv, dbStore, connectorReg, cfg.Server.PublicURL); err != nil {
			log.Bg().Error("slack connectors reconciler init failed", "error", err)
			os.Exit(1)
		} else if mgr != nil {
			go func() {
				if err := mgr.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Bg().Error("slack connectors reconciler exited", "error", err)
				}
			}()
		}
	}

	// Discord workspace-dimension reconciler. Opt-in via PARSAR_DISCORD_CONNECTORS=
	// true. Reads workspace_im_connectors and keeps one Gateway WebSocket runner
	// per workspace|app_id — the DB-driven twin of buildDiscordRunner.
	if dbStore != nil {
		if mgr, err := buildDiscordInboundManager(os.Getenv, dbStore, connectorReg, cfg.Server.PublicURL); err != nil {
			log.Bg().Error("discord connectors reconciler init failed", "error", err)
			os.Exit(1)
		} else if mgr != nil {
			go func() {
				if err := mgr.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Bg().Error("discord connectors reconciler exited", "error", err)
				}
			}()
		}
	}

	// Feishu outbound poll worker. Opt-in via PARSAR_FEISHU_OUTBOUND=true.
	// Poll interval default 10s; override via
	// PARSAR_FEISHU_OUTBOUND_POLL_SECONDS for load-shedding.
	if dbStore != nil {
		// Standalone binder for outbound's card-write path. Stamps the
		// agent_daemon device id onto each inflight slot so the
		// card-callback path (Phase 2 owner routing) can resolve the
		// owning pod without re-walking the binding tree.
		outboundBinder := agentdaemonbinding.NewPgBinder(pool, func(format string, args ...any) {
			log.Bg().Warn("inflight binder", "msg", fmt.Sprintf(format, args...))
		})
		if worker, err := buildFeishuOutboundWorker(os.Getenv, dbStore, connectorReg, outboundBinder, cfg.Server.PublicURL); err != nil {
			log.Bg().Error("feishu outbound worker init failed", "error", err)
			os.Exit(1)
		} else if worker != nil {
			// Bind the internal history endpoint to the live worker: it resolves
			// the per-conversation channel adapter (Feishu per-call, others from
			// the registry) and type-asserts the HistoryFetcher capability.
			imHistoryResolver.Set(worker)
			go func() {
				if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Bg().Error("feishu outbound worker exited", "error", err)
				}
			}()
		}
	}

	server := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Bg().Info("parsar server starting", "addr", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Bg().Error("parsar server stopped", "error", err)
			drainOTLPReceiver(otlpReceiver)
			drainAudit(auditIngester)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Bg().Info("parsar server shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Bg().Warn("http server shutdown error", "error", err)
		}
		// Drain order matters: HTTP server first (so in-flight
		// requests finish their audit emits), OTLP receiver next
		// (so in-flight POSTs reach the ingester buffer), audit
		// ingester (flush buffer) last.
		drainOTLPReceiver(otlpReceiver)
		drainAudit(auditIngester)
	}
}

func shouldRegisterFeishuAppProvisioning(cfg config.Config) bool {
	return strings.TrimSpace(cfg.Secret.MasterKey) != ""
}

func decideFeishuStartup(env func(string) string) feishuStartupDecision {
	if env == nil {
		env = os.Getenv
	}
	if feishu.IsMockEnabled(env) {
		return feishuStartupDecision{
			Mode:                    feishuStartupModeMock,
			RegisterOAuthHandlers:   true,
			RegisterWebhookSecurity: true,
		}
	}
	oauthConfigured := feishu.IsConfigured(env)
	webhookConfigured := feishu.IsWebhookConfigured(env)
	if !oauthConfigured && !webhookConfigured {
		return feishuStartupDecision{Mode: feishuStartupModeDisabled}
	}
	return feishuStartupDecision{
		Mode:                    feishuStartupModeProd,
		RegisterOAuthHandlers:   oauthConfigured,
		RegisterWebhookSecurity: webhookConfigured,
	}
}

func buildAuthProviderRegistry(env func(string) string, cfg config.Config, feishuDecision feishuStartupDecision, oidcStatuses []authoidc.ProviderEnvStatus) dev.AuthProviderRegistry {
	if env == nil {
		env = os.Getenv
	}
	providers := []dev.AuthProvider{{
		ID:         "password",
		Type:       dev.AuthProviderTypePassword,
		Label:      "Email password",
		Enabled:    true,
		Configured: true,
		LoginURL:   "/login",
	}}

	required := []string{
		feishu.EnvAppID,
		feishu.EnvAppSecret,
		feishu.EnvRedirectURI,
	}
	missing := missingEnv(env, required)
	configured := feishuDecision.RegisterOAuthHandlers
	if feishu.IsMockEnabled(env) {
		missing = nil
		configured = true
	}
	callbackURL := strings.TrimSpace(env(feishu.EnvRedirectURI))
	if callbackURL == "" {
		callbackURL = cfg.BuildPublicURL("/api/v1/auth/feishu/callback")
	}
	feishuProvider := dev.AuthProvider{
		ID:          "feishu",
		Type:        dev.AuthProviderTypeOAuth,
		Label:       "Feishu",
		Enabled:     feishuDecision.RegisterOAuthHandlers,
		Configured:  configured,
		CallbackURL: callbackURL,
		RequiredEnv: required,
		MissingEnv:  missing,
		DocsURL:     "docs/deploy/feishu-prod.md",
	}
	if feishuProvider.Enabled {
		feishuProvider.LoginURL = "/api/v1/auth/feishu/start"
	}
	providers = append(providers, feishuProvider)

	for _, status := range oidcStatuses {
		providerID := status.Config.ID
		enabled := len(status.MissingEnv) == 0
		provider := dev.AuthProvider{
			ID:          "oidc:" + providerID,
			Type:        dev.AuthProviderTypeOIDC,
			Label:       status.Config.Label,
			Enabled:     enabled,
			Configured:  enabled,
			CallbackURL: status.Config.RedirectURI,
			RequiredEnv: status.RequiredEnv,
			MissingEnv:  status.MissingEnv,
			DocsURL:     "docs/deploy/oidc-sso.md",
		}
		if provider.Enabled {
			provider.LoginURL = "/api/v1/auth/oidc/" + providerID + "/start"
		}
		providers = append(providers, provider)
	}

	return dev.AuthProviderRegistry{Providers: providers}
}

func missingEnv(env func(string) string, names []string) []string {
	missing := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(env(name)) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func drainAudit(ing *audit.Ingester) {
	if ing == nil {
		return
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ing.Stop(drainCtx); err != nil {
		stats := ing.Stats()
		log.Bg().Warn("audit ingester drain incomplete",
			"error", err,
			"buffer_len_remaining", stats.BufferLen,
			"dropped", stats.Dropped,
			"sink_errors", stats.SinkErrors,
		)
	}
}

// drainOTLPReceiver shuts down the embedded OTLP/HTTP receiver. Must
// be called BEFORE drainAudit so any OTLP request still in flight has
// a chance to land its events on the ingester buffer before the
// ingester closes. Nil-safe so the disabled-receiver path is a no-op.
func drainOTLPReceiver(r *otlp.Receiver) {
	if r == nil {
		return
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Shutdown(drainCtx); err != nil {
		log.Bg().Warn("otlp receiver shutdown error", "error", err)
	}
}

// runtimeHeartbeatSweeperStore is the tiny store surface needed by the
// runtime liveness sweeper. Narrower than *store.Store so startup
// config tests stay DB-free.
type runtimeHeartbeatSweeperStore interface {
	SweepStaleRuntimes(ctx context.Context, cutoff time.Time) (int64, error)
}

// buildRuntimeHeartbeatSweeper constructs the runtime heartbeat sweeper
// from env. Defaults match agent_daemon: 15s heartbeat, 60s timeout.
//
// Env contract:
//   - PARSAR_RUNTIME_HEARTBEAT_STALE_SECONDS (default 60; <= 0 disables)
//   - PARSAR_RUNTIME_HEARTBEAT_SWEEP_INTERVAL_SECONDS (optional;
//     default = stale / 4 floored at 15s)
func buildRuntimeHeartbeatSweeper(
	env func(string) string,
	runtimeStore runtimeHeartbeatSweeperStore,
) *runtimesweeper.Sweeper {
	if runtimeStore == nil {
		return nil
	}
	const defaultStaleSeconds = 60
	staleSeconds := defaultStaleSeconds
	if raw := strings.TrimSpace(env("PARSAR_RUNTIME_HEARTBEAT_STALE_SECONDS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			log.Bg().Warn("PARSAR_RUNTIME_HEARTBEAT_STALE_SECONDS invalid; runtime heartbeat sweep disabled",
				"value", raw, "err", err.Error())
			return nil
		}
		staleSeconds = n
	}
	if staleSeconds <= 0 {
		log.Bg().Info("runtime heartbeat sweep disabled by PARSAR_RUNTIME_HEARTBEAT_STALE_SECONDS",
			"value", staleSeconds)
		return nil
	}

	intervalSeconds := 0
	if raw := strings.TrimSpace(env("PARSAR_RUNTIME_HEARTBEAT_SWEEP_INTERVAL_SECONDS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			log.Bg().Warn("PARSAR_RUNTIME_HEARTBEAT_SWEEP_INTERVAL_SECONDS invalid; falling back to derived interval",
				"value", raw)
		} else {
			intervalSeconds = n
		}
	}
	opts := runtimesweeper.Options{
		StaleAfter: time.Duration(staleSeconds) * time.Second,
	}
	if intervalSeconds > 0 {
		opts.Interval = time.Duration(intervalSeconds) * time.Second
	}
	sw, err := runtimesweeper.New(runtimeStore, opts)
	if err != nil {
		log.Bg().Error("runtime heartbeat sweeper construct failed", "error", err)
		return nil
	}
	log.Bg().Info("runtime heartbeat sweeper configured", "sweeper", sw.String())
	return sw
}

// buildScheduler constructs the scheduled-task scheduler from env. Disabled by
// PARSAR_SCHEDULER_ENABLED=false; tunables (optional, defaulted in
// scheduler.New): PARSAR_SCHEDULER_INTERVAL_SECONDS,
// PARSAR_SCHEDULER_CLAIM_STALE_SECONDS, PARSAR_SCHEDULER_CLAIM_BATCH.
func buildScheduler(env func(string) string, st scheduler.Store) *scheduler.Scheduler {
	if st == nil {
		return nil
	}
	if raw := strings.TrimSpace(env("PARSAR_SCHEDULER_ENABLED")); raw != "" && !truthy(raw) {
		log.Bg().Info("scheduled task scheduler disabled by PARSAR_SCHEDULER_ENABLED")
		return nil
	}
	opts := scheduler.Options{}
	if raw := strings.TrimSpace(env("PARSAR_SCHEDULER_INTERVAL_SECONDS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			opts.Interval = time.Duration(n) * time.Second
		}
	}
	if raw := strings.TrimSpace(env("PARSAR_SCHEDULER_CLAIM_STALE_SECONDS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			opts.ClaimStaleAfter = time.Duration(n) * time.Second
		}
	}
	if raw := strings.TrimSpace(env("PARSAR_SCHEDULER_CLAIM_BATCH")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			opts.ClaimBatch = int32(n)
		}
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		opts.ClaimedBy = "scheduler@" + host
	}
	sc, err := scheduler.New(st, opts)
	if err != nil {
		log.Bg().Error("scheduler construct failed", "error", err)
		return nil
	}
	log.Bg().Info("scheduled task scheduler configured", "scheduler", sc.String())
	return sc
}

// buildAgentDaemonWSURL returns the wss://.../agent-daemon/ws URL the
// daemon dials after bootstrap. Derives from cfg.Server.PublicURL by
// swapping http→ws / https→wss; dev falls back to 127.0.0.1:18080.
// Production boot without public_url aborts in config.validate.go.
func buildAgentDaemonWSURL(cfg config.Config) string {
	const path = "/agent-daemon/ws"
	publicURL := strings.TrimSpace(cfg.Server.PublicURL)
	publicURL = strings.TrimRight(publicURL, "/")
	if publicURL == "" {
		return "ws://127.0.0.1:18080" + path
	}
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Host == "" {
		// Best-effort dev fallback so a hand-rolled public_url doesn't
		// crash gateway wiring; Validate would catch this in prod.
		return "ws://" + publicURL + path
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		parsed.Scheme = "wss"
	default:
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String()
}

func resolveAgentDaemonOwnerPodID(env func(string) string) string {
	if env == nil {
		env = os.Getenv
	}
	for _, key := range []string{"PARSAR_AGENT_DAEMON_POD_ID", "POD_NAME", "HOSTNAME"} {
		if value := strings.TrimSpace(env(key)); value != "" {
			return value
		}
	}
	if hostname, err := os.Hostname(); err == nil && strings.TrimSpace(hostname) != "" {
		return strings.TrimSpace(hostname)
	}
	return "parsar-pod-unknown"
}

func resolveAgentDaemonOwnerURL(env func(string) string, cfg config.Config) (string, error) {
	if env == nil {
		env = os.Getenv
	}
	if value := strings.TrimRight(strings.TrimSpace(env("PARSAR_AGENT_DAEMON_OWNER_URL")), "/"); value != "" {
		return value, nil
	}
	if podIP := strings.TrimSpace(env("POD_IP")); podIP != "" {
		return "http://" + net.JoinHostPort(podIP, resolveListenPort(cfg.Server.Addr)), nil
	}
	// No fallback to cfg.Server.PublicURL or 127.0.0.1: both are unsafe
	// in multi-replica deployments. PublicURL points at the ingress,
	// which load-balances stream-prompt forwards across replicas and
	// trips `stale_owner` checks; 127.0.0.1 only works when the owner
	// pod and the forwarding pod are the same process.
	return "", fmt.Errorf("agent_daemon owner URL not resolvable: set POD_IP (downward API: status.podIP) or PARSAR_AGENT_DAEMON_OWNER_URL")
}

func resolveListenPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err == nil && strings.TrimSpace(port) != "" {
		return strings.TrimSpace(port)
	}
	if strings.HasPrefix(addr, ":") && strings.TrimSpace(strings.TrimPrefix(addr, ":")) != "" {
		return strings.TrimSpace(strings.TrimPrefix(addr, ":"))
	}
	lastColon := strings.LastIndex(addr, ":")
	if lastColon >= 0 && lastColon < len(addr)-1 {
		candidate := strings.TrimSpace(addr[lastColon+1:])
		if candidate != "" && !strings.Contains(candidate, "]") {
			return candidate
		}
	}
	return "8080"
}

func resolveAgentDaemonInternalToken(env func(string) string, masterKey string) string {
	if env == nil {
		env = os.Getenv
	}
	if token := strings.TrimSpace(env("PARSAR_AGENT_DAEMON_INTERNAL_TOKEN")); token != "" {
		return token
	}
	if strings.TrimSpace(masterKey) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("parsar:agent-daemon-internal-routing:" + strings.TrimSpace(masterKey)))
	return fmt.Sprintf("%x", sum[:])
}

const envRuntimeProfile = "PARSAR_RUNTIME_PROFILE"

func resolveRuntimeProfile(env func(string) string, managedSandboxProvider bool) string {
	if env == nil {
		env = os.Getenv
	}
	raw := strings.ToLower(strings.TrimSpace(env(envRuntimeProfile)))
	switch raw {
	case "oss", "managed", "selfhost":
		return raw
	case "":
		if managedSandboxProvider {
			return "managed"
		}
		return "oss"
	default:
		log.Bg().Warn("PARSAR_RUNTIME_PROFILE invalid; falling back to runtime wiring", "value", raw)
		if managedSandboxProvider {
			return "managed"
		}
		return "oss"
	}
}

type configuredSandboxProber struct{}

func (configuredSandboxProber) Ping(ctx context.Context) error {
	return ctx.Err()
}

// buildAgentDaemonSandboxProvider wires the lazy-create SandboxProvider
// for the agent_daemon connector. Returns nil when sandbox mode is not
// configured (caller falls back to NoopSandboxProvider).
//
// Required env vars:
//   - AGENT_DAEMON_SANDBOX_TEMPLATE — e2b template id; empty -> disabled.
//   - PARSAR_E2B_API_KEY          — shared with opencode; empty -> disabled.
//
// Optional:
//   - PARSAR_E2B_API_BASE_URL — self-hosted e2b api base.
func buildAgentDaemonSandboxProvider(
	env func(string) string,
	cfg config.Config,
	dbStore *store.Store,
	registry *agentdaemongateway.Registry,
	binder agentdaemonbinding.Binder,
	selfPodID string,
) connagentdaemon.SandboxProvider {
	if env == nil {
		env = os.Getenv
	}
	// Local-docker backend short-circuit: when AGENT_DAEMON_SANDBOX_BACKEND
	// is "docker" this returns a container-backed provider and we skip the
	// e2b-specific API-key/CA/pod-IP wiring below entirely.
	if p := buildDockerAgentDaemonSandboxProvider(env, cfg, dbStore, registry, binder, selfPodID); p != nil {
		return p
	}
	template := strings.TrimSpace(env("AGENT_DAEMON_SANDBOX_TEMPLATE"))
	if template == "" {
		return nil
	}
	// Optional XL template — agents with config.sandbox_size = "xl" get
	// routed here instead of the default. Standard (4c8g) is always the
	// fallback; if XL is not configured an agent that requests "xl" will
	// silently fall back to the standard template (with a warn log) so a
	// missing XL pool degrades gracefully rather than failing acquires.
	templateXL := strings.TrimSpace(env("AGENT_DAEMON_SANDBOX_TEMPLATE_XL"))
	templates := map[string]string{
		"standard": template,
	}
	if templateXL != "" {
		templates["xl"] = templateXL
	}
	apiKey := strings.TrimSpace(env("PARSAR_E2B_API_KEY"))
	if apiKey == "" {
		log.Bg().Warn("agent_daemon sandbox mode disabled: AGENT_DAEMON_SANDBOX_TEMPLATE set but PARSAR_E2B_API_KEY is missing",
			"template", template)
		return nil
	}
	publicURL := strings.TrimSpace(cfg.Server.PublicURL)
	if publicURL == "" {
		// Dev fallback mirrors buildAgentDaemonWSURL.
		publicURL = "http://127.0.0.1:18080"
	}
	apiBaseURL := strings.TrimSpace(env("PARSAR_E2B_API_BASE_URL"))
	client := &e2bsandbox.Client{
		APIKey:            apiKey,
		APIBaseURL:        apiBaseURL,
		SandboxHost:       strings.TrimSpace(env("PARSAR_E2B_SANDBOX_HOST")),
		DefaultTemplateID: template,
	}
	// If the internal sandbox gateway uses a private CA, inject it into
	// the E2B client's TLS trust pool so Go can verify the certificate.
	if caPEM := strings.TrimSpace(env("PARSAR_E2B_CA_CERT")); caPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			log.Bg().Warn("PARSAR_E2B_CA_CERT: failed to parse PEM certificate; TLS may fail")
		} else {
			client.HTTPClient = &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{RootCAs: pool},
				},
			}
			log.Bg().Info("agent_daemon sandbox: custom CA cert loaded for E2B client")
		}
	}
	// In-cluster pod IP resolver: when running inside K8s, resolve the
	// sandbox pod's cluster-internal IP so envd RunCommand calls bypass
	// the external gateway and connect directly via the pod network.
	// Prefer an explicit token (PARSAR_K8S_POD_TOKEN) pointed at the
	// sandbox cluster's API server (PARSAR_K8S_API_SERVER); fall back
	// to in-cluster SA when neither is set.
	var podIPResolver *e2bsandbox.PodIPResolver
	if tok := strings.TrimSpace(env("PARSAR_K8S_POD_TOKEN")); tok != "" {
		apiServer := strings.TrimSpace(env("PARSAR_K8S_API_SERVER"))
		podIPResolver = e2bsandbox.NewPodIPResolver(apiServer, tok, nil, true)
		if podIPResolver != nil {
			log.Bg().Info("agent_daemon sandbox: pod IP resolver using explicit token",
				"api_server", apiServer)
		}
	}
	if podIPResolver == nil {
		if r := e2bsandbox.NewInClusterPodIPResolver(); r != nil {
			podIPResolver = r
			log.Bg().Info("agent_daemon sandbox: pod IP resolver using in-cluster SA token")
		}
	}
	provider, err := connagentdaemon.NewE2BSandboxProvider(connagentdaemon.E2BProviderConfig{
		Client:        client,
		Store:         dbStore,
		Registry:      registry,
		Binder:        binder,
		Bindings:      dbStore,
		Template:      template,
		Templates:     templates,
		DefaultSize:   "standard",
		ServerURL:     publicURL,
		PodIPResolver: podIPResolver,
		OwnerChecker:  dbStore,
		SelfPodID:     selfPodID,
		Log:           log.Bg(),
	})
	if err != nil {
		log.Bg().Warn("agent_daemon sandbox provider init failed; sandbox mode disabled",
			"error", err)
		return nil
	}
	log.Bg().Info("agent_daemon sandbox provider wired",
		"template", template,
		"template_xl", templateXL,
		"server_url", publicURL)
	return provider
}

// openPool opens the shared pgxpool used by both the audit ingester and
// the store. Returns nil when databaseURL is empty so dev environments
// without a database can still bring up the health endpoint.
func openPool(databaseURL string) *pgxpool.Pool {
	if strings.TrimSpace(databaseURL) == "" {
		log.Bg().Warn("DATABASE_URL is not set; database-backed dev endpoints are disabled")
		return nil
	}
	pool, err := db.OpenPool(context.Background(), databaseURL)
	if err != nil {
		log.Bg().Warn("failed to connect database; database-backed dev endpoints are disabled", "error", err)
		return nil
	}
	return pool
}

// buildStore constructs the *store.Store and the audit ingester from a
// shared pgxpool. Returns (nil, nil) when the pool is unavailable, so
// the caller can degrade gracefully without panicking.
//
// When fanoutEndpoint is non-empty, the ingester's sink becomes a
// MultiSink composed of the canonical PostgresSink (authoritative —
// the audit_records write that must never fail silently) plus an
// OTelExporterSink that ships every event to a customer-owned OTel
// collector. Fan-out errors are logged + swallowed; only the
// PostgresSink result drives ingester success metrics. Empty
// fanoutEndpoint keeps the legacy single-sink behaviour.
func buildStore(pool *pgxpool.Pool, fanoutEndpoint string) (*store.Store, *audit.Ingester) {
	if pool == nil {
		return nil, nil
	}
	queries := sqlc.New(pool)
	canonical := audit.NewPostgresSink(queries)

	var sink audit.Sink = canonical
	if endpoint := strings.TrimSpace(fanoutEndpoint); endpoint != "" {
		exporter, err := audit.NewOTelExporterSink(audit.OTelExporterOptions{
			Endpoint: endpoint,
		})
		if err != nil {
			log.Bg().Warn("audit OTLP fan-out exporter init failed; running without fan-out",
				"endpoint", endpoint, "error", err)
		} else {
			multi, err := audit.NewMultiSink(func(format string, args ...any) {
				log.Bg().Warn(fmt.Sprintf(format, args...),
					"subsystem", "audit_otel_fanout")
			}, canonical, exporter)
			if err != nil {
				log.Bg().Warn("audit MultiSink init failed; running without fan-out",
					"error", err)
			} else {
				sink = multi
				// Log host-only to avoid leaking the full collector URL
				// (path/query/internal subdomain) into INFO startup
				// logs; cfg.Redacted() carries the full endpoint.
				log.Bg().Info("audit OTLP fan-out wired",
					"endpoint_host", fanoutEndpointHost(endpoint))
			}
		}
	}

	ing := audit.NewIngester(sink, audit.Options{})
	return store.New(pool, store.WithAudit(ing)), ing
}

// fanoutEndpointHost extracts a host-only label so a customer-internal
// collector URL does not show up in INFO logs. Returns "<unparseable>"
// on bad input so callers get a non-empty field.
func fanoutEndpointHost(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return "<unparseable>"
	}
	return u.Host
}

type feishuSharedBotEnv struct {
	appID     string
	appSecret string
	botOpenID string
}

func (c feishuSharedBotEnv) configured() bool {
	return strings.TrimSpace(c.appID) != "" && strings.TrimSpace(c.appSecret) != ""
}

func defaultFeishuSharedBotEnv(env func(string) string) feishuSharedBotEnv {
	appID := strings.TrimSpace(env("PARSAR_FEISHU_DEFAULT_BOT_APP_ID"))
	appSecret := strings.TrimSpace(env("PARSAR_FEISHU_DEFAULT_BOT_APP_SECRET"))
	botOpenID := strings.TrimSpace(env("PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID"))
	if appID != "" || appSecret != "" {
		return feishuSharedBotEnv{appID: appID, appSecret: appSecret, botOpenID: botOpenID}
	}
	if botOpenID == "" {
		botOpenID = strings.TrimSpace(env("PARSAR_FEISHU_BOT_OPEN_ID"))
	}
	return feishuSharedBotEnv{
		appID:     strings.TrimSpace(env("PARSAR_FEISHU_APP_ID")),
		appSecret: strings.TrimSpace(env("PARSAR_FEISHU_APP_SECRET")),
		botOpenID: botOpenID,
	}
}

// buildFeishuWebSocketManager constructs the Feishu event-websocket inbound
// manager for DB-driven workspace connectors and optional legacy env bots.
//
// Env contract:
//   - PARSAR_FEISHU_WS_REFRESH_SECONDS       (optional; default 30, cap 600)
//   - PARSAR_FEISHU_OPENAPI_BASE_URL         (optional; default SDK domain)
//   - PARSAR_FEISHU_DEFAULT_BOT_APP_ID       (optional; falls back to PARSAR_FEISHU_APP_ID)
//   - PARSAR_FEISHU_DEFAULT_BOT_APP_SECRET   (optional; falls back to PARSAR_FEISHU_APP_SECRET)
//   - PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID      (optional; self-message dedup)
//   - PARSAR_MASTER_KEY                      (REQUIRED when enabled)
func buildFeishuWebSocketManager(env func(string) string, dbStore *store.Store, connectorReg *connector.Registry, publicURL string) (*inbound.Manager, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return nil, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("feishu websocket inbound: init secrets service: %w", err)
	}

	refreshEvery := 30 * time.Second
	if raw := strings.TrimSpace(env("PARSAR_FEISHU_WS_REFRESH_SECONDS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			log.Bg().Warn("PARSAR_FEISHU_WS_REFRESH_SECONDS invalid; using default",
				"value", raw, "default_s", 30)
		} else if n > 600 {
			log.Bg().Warn("PARSAR_FEISHU_WS_REFRESH_SECONDS too high; capping",
				"value", n, "cap_s", 600)
			refreshEvery = 600 * time.Second
		} else {
			refreshEvery = time.Duration(n) * time.Second
		}
	}

	defaultBot := defaultFeishuSharedBotEnv(env)
	var feishuJoinURLBuilder func(string) string
	if strings.TrimSpace(publicURL) != "" {
		root := strings.TrimRight(strings.TrimSpace(publicURL), "/")
		feishuJoinURLBuilder = func(workspaceID string) string {
			return root + "/join-workspace?id=" + workspaceID + "&from=feishu"
		}
	}
	manager, err := inbound.NewManager(inbound.Options{
		Store:           dbStore,
		Secrets:         secretsSvc,
		Logger:          slogFeishuInboundLogger{},
		RefreshInterval: refreshEvery,
		Domain:          strings.TrimSpace(env("PARSAR_FEISHU_OPENAPI_BASE_URL")),
		OpenAPIBaseURL:  strings.TrimSpace(env("PARSAR_FEISHU_OPENAPI_BASE_URL")),
		DefaultSharedBot: inbound.DefaultSharedBotConfig{
			AppID:     defaultBot.appID,
			AppSecret: defaultBot.appSecret,
			BotOpenID: defaultBot.botOpenID,
		},
		PermissionRouter:          inboundPermissionRouter{registry: connectorReg},
		PromptForUserChoiceRouter: inboundPromptForUserChoiceRouter{registry: connectorReg},
		JoinURLBuilder:            feishuJoinURLBuilder,
		Connectors:                dbStore,
	})
	if err != nil {
		return nil, fmt.Errorf("feishu websocket inbound: init manager: %w", err)
	}
	log.Bg().Info("feishu websocket inbound manager configured",
		"refresh_interval", refreshEvery,
		"default_shared_bot", defaultBot.configured(),
		"permission_router", connectorReg != nil,
	)
	return manager, nil
}

// buildSlackRunner constructs the Slack Socket Mode inbound runner from env.
// Returns nil (silent skip) when the feature flag is off, mirroring
// buildFeishuWebSocketManager.
//
// Env contract:
//   - PARSAR_SLACK_SOCKET        (default off; "true" enables)
//   - PARSAR_SLACK_BOT_TOKEN     (REQUIRED when enabled; xoxb-… Web API token)
//   - PARSAR_SLACK_APP_TOKEN     (REQUIRED when enabled; xapp-… connections:write)
//   - PARSAR_SLACK_APP_ID        (optional; the neutral bot id stamped on events)
//
// N4 is the env-gated shared-bot path: a single workspace's tokens, no DB
// config (that is N5). The runner shares the router store and feeds the same
// neutral pipeline as Feishu. publicURL seeds the visibility-rejection join
// link, matching the Feishu manager's JoinURLBuilder.
//
// Card-action routing (N10): when PARSAR_MASTER_KEY is set we build a neutral
// inbound.Manager (same Store/Secrets/PermissionRouter/PromptForUserChoiceRouter
// as Feishu) and inject its CardActionRouter so Slack button clicks resolve
// permissions / choices / credential forms. Without a master key the adapter
// runs router-less and a click just echoes a neutral "received" reply.
func buildSlackRunner(env func(string) string, dbStore *store.Store, connectorReg *connector.Registry, publicURL string) (*slackrunner.Runner, error) {
	if !truthy(env("PARSAR_SLACK_SOCKET")) {
		return nil, nil
	}
	botToken := strings.TrimSpace(env("PARSAR_SLACK_BOT_TOKEN"))
	if botToken == "" {
		return nil, errors.New("PARSAR_SLACK_SOCKET=true requires PARSAR_SLACK_BOT_TOKEN (xoxb-… Web API token)")
	}
	appToken := strings.TrimSpace(env("PARSAR_SLACK_APP_TOKEN"))
	if appToken == "" {
		return nil, errors.New("PARSAR_SLACK_SOCKET=true requires PARSAR_SLACK_APP_TOKEN (xapp-… connections:write token)")
	}
	appID := strings.TrimSpace(env("PARSAR_SLACK_APP_ID"))

	var slackJoinURLBuilder func(string) string
	if strings.TrimSpace(publicURL) != "" {
		root := strings.TrimRight(strings.TrimSpace(publicURL), "/")
		slackJoinURLBuilder = func(workspaceID string) string {
			return root + "/join-workspace?id=" + workspaceID + "&from=slack"
		}
	}

	// Best-effort card-action router: needs the secret vault (credential-form
	// encryption) plus the connector-backed permission/choice routers. Absent a
	// master key the adapter stays router-less rather than failing inbound.
	var adapterOpts []slackchannel.Option
	if masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY")); masterKey != "" {
		secretsSvc, err := secrets.New(masterKey)
		if err != nil {
			return nil, fmt.Errorf("slack socket mode: init secrets service: %w", err)
		}
		actionMgr, err := inbound.NewManager(inbound.Options{
			Store:                     dbStore,
			Secrets:                   secretsSvc,
			Logger:                    slogFeishuInboundLogger{},
			JoinURLBuilder:            slackJoinURLBuilder,
			PermissionRouter:          inboundPermissionRouter{registry: connectorReg},
			PromptForUserChoiceRouter: inboundPromptForUserChoiceRouter{registry: connectorReg},
		})
		if err != nil {
			return nil, fmt.Errorf("slack socket mode: init action manager: %w", err)
		}
		adapterOpts = append(adapterOpts, slackchannel.WithActionRouter(actionMgr.CardActionRouter()))
	} else {
		log.Bg().Warn("slack socket mode: PARSAR_MASTER_KEY unset; button clicks echo 'received' (no permission/choice/credential routing)")
	}

	// Per-team bot-token resolver: a button click or runner-side send for a
	// workspace other than the env-token default resolves its xoxb from the
	// kind='slack_bot' secret (keyed by Slack team_id), with the env token as
	// the single-tenant fallback. nil when no master key — the adapter keeps
	// the static env resolver.
	slackResolver, err := buildSlackCredentialResolver(env, dbStore)
	if err != nil {
		return nil, fmt.Errorf("slack socket mode: build credential resolver: %w", err)
	}
	if slackResolver != nil {
		adapterOpts = append(adapterOpts, slackchannel.WithCredentialResolver(slackResolver))
	}

	adapter := slackchannel.New(slackchannel.Config{
		AppID:    appID,
		BotToken: botToken,
		AppToken: appToken,
	}, adapterOpts...)
	runner, err := slackrunner.New(slackrunner.Config{
		BotToken:   botToken,
		AppToken:   appToken,
		Channel:    adapter,
		Store:      dbStore,
		GateConfig: gatewaypkg.GateConfig{JoinURLBuilder: slackJoinURLBuilder},
	})
	if err != nil {
		return nil, fmt.Errorf("slack socket mode: init runner: %w", err)
	}
	log.Bg().Info("slack socket mode runner configured",
		"app_id", appID != "",
		"action_router", len(adapterOpts) > 0)
	return runner, nil
}

// buildDiscordRunner constructs the Discord Gateway WebSocket inbound runner
// from env. Returns nil (silent skip) when the feature flag is off, mirroring
// buildSlackRunner.
//
// Env contract:
//   - PARSAR_DISCORD_GATEWAY      (default off; "true" enables)
//   - PARSAR_DISCORD_BOT_TOKEN    (REQUIRED when enabled; the bot token)
//   - PARSAR_DISCORD_APP_ID       (optional; the Discord application id, used as
//     the neutral bot id stamped on events with no guild)
//
// Like Slack this is the env-gated shared-bot path: a single bot's token feeding
// the same neutral pipeline as Feishu/Slack. publicURL seeds the
// visibility-rejection join link. Card-action routing follows the Slack shape:
// with PARSAR_MASTER_KEY set we build a neutral inbound.Manager and inject its
// CardActionRouter so Discord button clicks resolve permissions / choices /
// credential forms; without it the adapter runs router-less and a click echoes a
// neutral "received" reply. A MemoryPickStore is always injected so the
// per-interaction select-pick accumulation (Discord fires one interaction per
// select change) folds into the Submit click.
func buildDiscordRunner(env func(string) string, dbStore *store.Store, connectorReg *connector.Registry, publicURL string) (*discordrunner.Runner, error) {
	if !truthy(env("PARSAR_DISCORD_GATEWAY")) {
		return nil, nil
	}
	botToken := strings.TrimSpace(env("PARSAR_DISCORD_BOT_TOKEN"))
	if botToken == "" {
		return nil, errors.New("PARSAR_DISCORD_GATEWAY=true requires PARSAR_DISCORD_BOT_TOKEN")
	}
	appID := strings.TrimSpace(env("PARSAR_DISCORD_APP_ID"))

	var discordJoinURLBuilder func(string) string
	if strings.TrimSpace(publicURL) != "" {
		root := strings.TrimRight(strings.TrimSpace(publicURL), "/")
		discordJoinURLBuilder = func(guildID string) string {
			return root + "/join-workspace?id=" + guildID + "&from=discord"
		}
	}

	// The pick store folds the separate select-change interactions Discord
	// delivers into the Submit click. The adapter owns no live state, so the
	// runner injects one regardless of routing.
	adapterOpts := []discordchannel.Option{discordchannel.WithPickStore(discordchannel.NewMemoryPickStore())}
	if masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY")); masterKey != "" {
		secretsSvc, err := secrets.New(masterKey)
		if err != nil {
			return nil, fmt.Errorf("discord gateway: init secrets service: %w", err)
		}
		actionMgr, err := inbound.NewManager(inbound.Options{
			Store:                     dbStore,
			Secrets:                   secretsSvc,
			Logger:                    slogFeishuInboundLogger{},
			JoinURLBuilder:            discordJoinURLBuilder,
			PermissionRouter:          inboundPermissionRouter{registry: connectorReg},
			PromptForUserChoiceRouter: inboundPromptForUserChoiceRouter{registry: connectorReg},
		})
		if err != nil {
			return nil, fmt.Errorf("discord gateway: init action manager: %w", err)
		}
		adapterOpts = append(adapterOpts, discordchannel.WithActionRouter(actionMgr.CardActionRouter()))
	} else {
		log.Bg().Warn("discord gateway: PARSAR_MASTER_KEY unset; button clicks echo 'received' (no permission/choice/credential routing)")
	}

	// Per-guild bot-token resolver: a button click or runner-side send for a guild
	// other than the env-token default resolves its token from the
	// kind='discord_bot' secret (keyed by Discord guild_id), with the env token as
	// the single-bot fallback. nil when no master key — the adapter keeps the
	// static env resolver.
	discordResolver, err := buildDiscordCredentialResolver(env, dbStore)
	if err != nil {
		return nil, fmt.Errorf("discord gateway: build credential resolver: %w", err)
	}
	if discordResolver != nil {
		adapterOpts = append(adapterOpts, discordchannel.WithCredentialResolver(discordResolver))
	}

	adapter := discordchannel.New(discordchannel.Config{
		AppID:    appID,
		BotToken: botToken,
	}, adapterOpts...)
	runner, err := discordrunner.New(discordrunner.Config{
		BotToken:   botToken,
		Channel:    adapter,
		Store:      dbStore,
		GateConfig: gatewaypkg.GateConfig{JoinURLBuilder: discordJoinURLBuilder},
	})
	if err != nil {
		return nil, fmt.Errorf("discord gateway: init runner: %w", err)
	}
	log.Bg().Info("discord gateway runner configured",
		"app_id", appID != "",
		"action_router", len(adapterOpts) > 1) // >1: pick store is always present
	return runner, nil
}

// buildTeamsRunner constructs the Microsoft Teams webhook inbound runner from
// env. Returns nil (silent skip) when neither feature flag is set, mirroring
// buildSlackRunner/buildDiscordRunner. Unlike those, the returned runner is an
// http.Handler (Teams delivers inbound as HTTPS POSTs) — main.go mounts
// runner.Handler() on the chi router rather than launching a Run goroutine.
//
// Two modes share this one webhook:
//   - PARSAR_TEAMS_WEBHOOK=true    single env bot (PARSAR_TEAMS_APP_ID/APP_PASSWORD
//     required; the App Id is the fixed inbound JWT audience and outbound client_id).
//   - PARSAR_TEAMS_CONNECTORS=true DB-backed multi-tenant: one webhook serving
//     every workspace_im_connectors teams row. Requires PARSAR_MASTER_KEY (vault
//     seal). Each inbound request self-resolves: the JWT's own aud claim is checked
//     against the enabled connector set, and outbound replies decrypt that
//     workspace's app_password per call. Env creds become an optional fallback.
//
// Either flag mounts the webhook; when both are set the connector path layers in
// front of the env bot. Other env:
//   - PARSAR_TEAMS_TENANT_ID   (optional; pins a single-tenant token authority,
//     empty selects the multi-tenant botframework.com authority)
//
// Inbound vs outbound auth are asymmetric (the classic Bot Framework pitfall):
// the token verifier checks the inbound JWT bearer, while the outbound Connector
// transport mints an AAD client-credentials bearer from (app id, password). The
// two never share a token.
//
// Card-action routing mirrors the Slack shape: with PARSAR_MASTER_KEY set we
// build a neutral inbound.Manager and inject its CardActionRouter so Teams
// Adaptive Card button clicks resolve permissions / choices / credential forms;
// without it the adapter runs router-less and a click just echoes a neutral
// "received" ack.
func buildTeamsRunner(env func(string) string, dbStore *store.Store, connectorReg *connector.Registry, publicURL string) (*teamsrunner.Runner, error) {
	connectorsMode := truthy(env("PARSAR_TEAMS_CONNECTORS"))
	if !truthy(env("PARSAR_TEAMS_WEBHOOK")) && !connectorsMode {
		return nil, nil
	}
	appID := strings.TrimSpace(env("PARSAR_TEAMS_APP_ID"))
	appPassword := strings.TrimSpace(env("PARSAR_TEAMS_APP_PASSWORD"))
	if connectorsMode {
		if strings.TrimSpace(env("PARSAR_MASTER_KEY")) == "" {
			return nil, errors.New("PARSAR_TEAMS_CONNECTORS=true requires PARSAR_MASTER_KEY (same value the secret vault was sealed with)")
		}
	} else {
		// Single env bot: both credentials are required so a webhook POST that
		// fails Bot Framework auth is a 401 rather than silently trusted.
		if appID == "" {
			return nil, errors.New("PARSAR_TEAMS_WEBHOOK=true requires PARSAR_TEAMS_APP_ID (the Microsoft App Id)")
		}
		if appPassword == "" {
			return nil, errors.New("PARSAR_TEAMS_WEBHOOK=true requires PARSAR_TEAMS_APP_PASSWORD (the app secret for outbound Connector auth)")
		}
	}
	tenantID := strings.TrimSpace(env("PARSAR_TEAMS_TENANT_ID"))

	var teamsJoinURLBuilder func(string) string
	if strings.TrimSpace(publicURL) != "" {
		root := strings.TrimRight(strings.TrimSpace(publicURL), "/")
		teamsJoinURLBuilder = func(workspaceID string) string {
			return root + "/join-workspace?id=" + workspaceID + "&from=teams"
		}
	}

	// Inbound JWT verifier. Single-bot pins the one env App Id as the audience;
	// connectors mode validates each token's own aud against the enabled teams
	// connector set (+ env bot), so one webhook authenticates many workspace bots.
	var verifier teamschannel.TokenVerifier
	if connectorsMode {
		verifier = teamschannel.NewMultiTenantJWKSVerifier(storeAllowsTeamsAppID(dbStore, appID))
	} else {
		verifier = teamschannel.NewJWKSVerifier(appID)
	}
	adapterOpts := []teamschannel.Option{
		teamschannel.WithTokenVerifier(verifier),
	}

	// Connectors mode: layer the DB (app_id → app_password) resolver in front of
	// the env static so outbound replies use each workspace's own secret.
	if connectorsMode {
		credResolver, err := buildTeamsCredentialResolver(env, dbStore)
		if err != nil {
			return nil, fmt.Errorf("teams webhook: init credential resolver: %w", err)
		}
		if credResolver != nil {
			adapterOpts = append(adapterOpts, teamschannel.WithCredentialResolver(credResolver))
		}
	}

	// Best-effort card-action router: needs the secret vault (credential-form
	// encryption) plus the connector-backed permission/choice routers. Absent a
	// master key the adapter stays router-less rather than failing inbound.
	actionRouterWired := false
	if masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY")); masterKey != "" {
		secretsSvc, err := secrets.New(masterKey)
		if err != nil {
			return nil, fmt.Errorf("teams webhook: init secrets service: %w", err)
		}
		actionMgr, err := inbound.NewManager(inbound.Options{
			Store:                     dbStore,
			Secrets:                   secretsSvc,
			Logger:                    slogFeishuInboundLogger{},
			JoinURLBuilder:            teamsJoinURLBuilder,
			PermissionRouter:          inboundPermissionRouter{registry: connectorReg},
			PromptForUserChoiceRouter: inboundPromptForUserChoiceRouter{registry: connectorReg},
		})
		if err != nil {
			return nil, fmt.Errorf("teams webhook: init action manager: %w", err)
		}
		adapterOpts = append(adapterOpts, teamschannel.WithActionRouter(actionMgr.CardActionRouter()))
		actionRouterWired = true
	} else {
		log.Bg().Warn("teams webhook: PARSAR_MASTER_KEY unset; button clicks echo 'received' (no permission/choice/credential routing)")
	}

	adapter := teamschannel.New(teamschannel.Config{
		AppID:       appID,
		AppPassword: appPassword,
		TenantID:    tenantID,
	}, adapterOpts...)
	runner, err := teamsrunner.New(teamsrunner.Config{
		Channel:    adapter,
		Store:      dbStore,
		GateConfig: gatewaypkg.GateConfig{JoinURLBuilder: teamsJoinURLBuilder},
	})
	if err != nil {
		return nil, fmt.Errorf("teams webhook: init runner: %w", err)
	}
	log.Bg().Info("teams webhook runner configured",
		"connectors_mode", connectorsMode,
		"env_bot", appID != "",
		"tenant_pinned", tenantID != "",
		"action_router", actionRouterWired)
	return runner, nil
}

// buildSlackInboundManager constructs the workspace-dimension Slack Socket Mode
// reconciler: it scans workspace_im_connectors for enabled slack rows and keeps
// one Socket Mode runner per workspace|app_id, hot-reloading on token rotation.
// This is the DB-driven multi-tenant twin of buildSlackRunner's single env bot.
//
// The card-action router and the app_id credential resolver are built once and
// shared across every per-bot adapter the NewAdapter factory mints — both
// resolve by store lookup (card payload / app_id), not by a captured token, so a
// single instance serves all workspace bots.
func buildSlackInboundManager(env func(string) string, dbStore *store.Store, connectorReg *connector.Registry, publicURL string) (*slackrunner.Manager, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return nil, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("slack connectors: init secrets service: %w", err)
	}

	var slackJoinURLBuilder func(string) string
	if strings.TrimSpace(publicURL) != "" {
		root := strings.TrimRight(strings.TrimSpace(publicURL), "/")
		slackJoinURLBuilder = func(workspaceID string) string {
			return root + "/join-workspace?id=" + workspaceID + "&from=slack"
		}
	}

	actionMgr, err := inbound.NewManager(inbound.Options{
		Store:                     dbStore,
		Secrets:                   secretsSvc,
		Logger:                    slogFeishuInboundLogger{},
		JoinURLBuilder:            slackJoinURLBuilder,
		PermissionRouter:          inboundPermissionRouter{registry: connectorReg},
		PromptForUserChoiceRouter: inboundPromptForUserChoiceRouter{registry: connectorReg},
		Connectors:                dbStore,
	})
	if err != nil {
		return nil, fmt.Errorf("slack connectors: init action manager: %w", err)
	}
	actionRouterOpt := slackchannel.WithActionRouter(actionMgr.CardActionRouter())
	resolver, err := buildSlackCredentialResolver(env, dbStore)
	if err != nil {
		return nil, fmt.Errorf("slack connectors: build credential resolver: %w", err)
	}

	newAdapter := func(appID, botToken, appToken string) *slackchannel.Channel {
		opts := []slackchannel.Option{actionRouterOpt}
		// The Manager already decrypted this connector's bot token and hands it
		// in via Config.BotToken (the channel's default resolver returns it).
		// Only fall back to the shared per-team resolver when no per-connector
		// token was supplied — otherwise it would re-resolve by team_id, miss
		// the connector's app_id-keyed secret, and drop to an empty env token.
		if strings.TrimSpace(botToken) == "" && resolver != nil {
			opts = append(opts, slackchannel.WithCredentialResolver(resolver))
		}
		return slackchannel.New(slackchannel.Config{
			AppID:    appID,
			BotToken: botToken,
			AppToken: appToken,
		}, opts...)
	}

	mgr, err := slackrunner.NewManager(slackrunner.ManagerConfig{
		Store:       dbStore,
		RouterStore: dbStore,
		Secrets:     secretsSvc,
		NewAdapter:  newAdapter,
		GateConfig:  gatewaypkg.GateConfig{JoinURLBuilder: slackJoinURLBuilder},
	})
	if err != nil {
		return nil, fmt.Errorf("slack connectors: init manager: %w", err)
	}
	log.Bg().Info("slack connectors reconciler configured")
	return mgr, nil
}

// buildDiscordInboundManager is the Discord twin of buildSlackInboundManager: it
// scans workspace_im_connectors for enabled discord rows and keeps one Gateway
// WebSocket runner per workspace|app_id. Discord uses one bot token (no app token),
// so the NewAdapter factory takes only (appID, botToken). A MemoryPickStore is
// injected per adapter so select-change interactions fold into the Submit click.
func buildDiscordInboundManager(env func(string) string, dbStore *store.Store, connectorReg *connector.Registry, publicURL string) (*discordrunner.Manager, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return nil, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("discord connectors: init secrets service: %w", err)
	}

	var discordJoinURLBuilder func(string) string
	if strings.TrimSpace(publicURL) != "" {
		root := strings.TrimRight(strings.TrimSpace(publicURL), "/")
		discordJoinURLBuilder = func(workspaceID string) string {
			return root + "/join-workspace?id=" + workspaceID + "&from=discord"
		}
	}

	actionMgr, err := inbound.NewManager(inbound.Options{
		Store:                     dbStore,
		Secrets:                   secretsSvc,
		Logger:                    slogFeishuInboundLogger{},
		JoinURLBuilder:            discordJoinURLBuilder,
		PermissionRouter:          inboundPermissionRouter{registry: connectorReg},
		PromptForUserChoiceRouter: inboundPromptForUserChoiceRouter{registry: connectorReg},
		Connectors:                dbStore,
	})
	if err != nil {
		return nil, fmt.Errorf("discord connectors: init action manager: %w", err)
	}
	sharedActionRouter := actionMgr.CardActionRouter()
	resolver, err := buildDiscordCredentialResolver(env, dbStore)
	if err != nil {
		return nil, fmt.Errorf("discord connectors: build credential resolver: %w", err)
	}

	newAdapter := func(appID, botToken string) *discordchannel.Channel {
		opts := []discordchannel.Option{
			discordchannel.WithPickStore(discordchannel.NewMemoryPickStore()),
			discordchannel.WithActionRouter(sharedActionRouter),
		}
		// The Manager already decrypted this connector's bot token and hands it
		// in via Config.BotToken (the channel's default resolver returns it).
		// Only fall back to the shared guild-keyed resolver when no per-connector
		// token was supplied — otherwise it would re-resolve by guild_id, miss
		// the connector's app_id-keyed secret, and drop to an empty env token
		// ("missing bot token").
		if strings.TrimSpace(botToken) == "" && resolver != nil {
			opts = append(opts, discordchannel.WithCredentialResolver(resolver))
		}
		return discordchannel.New(discordchannel.Config{
			AppID:    appID,
			BotToken: botToken,
		}, opts...)
	}

	mgr, err := discordrunner.NewManager(discordrunner.ManagerConfig{
		Store:       dbStore,
		RouterStore: dbStore,
		Secrets:     secretsSvc,
		NewAdapter:  newAdapter,
		GateConfig:  gatewaypkg.GateConfig{JoinURLBuilder: discordJoinURLBuilder},
	})
	if err != nil {
		return nil, fmt.Errorf("discord connectors: init manager: %w", err)
	}
	log.Bg().Info("discord connectors reconciler configured")
	return mgr, nil
}

// inboundPermissionRouter adapts the connector registry to the
// inbound.PermissionRouter interface. The Feishu inbound
// package can't depend on `connector` directly, so this adapter
// lives in main where the wiring belongs.
//
// SubmitPermission iterates registered connectors because the card
// payload carries only permission_request_id; the owning connector
// returns nil, the rest return an error and we move on.
type inboundPermissionRouter struct {
	registry *connector.Registry
}

func (r inboundPermissionRouter) SubmitPermission(ctx context.Context, decision inbound.PermissionDecision) error {
	if r.registry == nil {
		return errors.New("permission router: connector registry not configured")
	}
	cd := connector.PermissionDecision{
		RequestID: decision.RequestID,
		Approved:  decision.Approved,
		Note:      decision.Note,
		By:        decision.OperatorID,
	}
	var lastErr error
	for _, name := range r.registry.Types() {
		conn, err := r.registry.Get(name)
		if err != nil || conn == nil {
			continue
		}
		if err := conn.SubmitPermission(ctx, cd); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("permission router: no connector accepted the verdict")
	}
	return lastErr
}

// inboundPromptForUserChoiceRouter is the ask-flow twin of
// inboundPermissionRouter: same fan-out-to-registered-connectors
// behaviour, different decision shape.
type inboundPromptForUserChoiceRouter struct {
	registry *connector.Registry
}

func (r inboundPromptForUserChoiceRouter) SubmitPromptForUserChoice(ctx context.Context, decision inbound.PromptForUserChoiceDecision) error {
	if r.registry == nil {
		return errors.New("prompt_for_user_choice router: connector registry not configured")
	}
	qas := make([]connector.PromptForUserChoiceQuestionAnswer, 0, len(decision.QuestionAnswers))
	for _, qa := range decision.QuestionAnswers {
		qas = append(qas, connector.PromptForUserChoiceQuestionAnswer{
			Header: qa.Header,
			Answer: qa.Answer,
		})
	}
	cd := connector.PromptForUserChoiceDecision{
		RequestID:       decision.RequestID,
		QuestionAnswers: qas,
		Answers:         decision.Answers,
		Cancelled:       decision.Cancelled,
		Reason:          decision.Reason,
		By:              decision.OperatorID,
	}
	var lastErr error
	for _, name := range r.registry.Types() {
		conn, err := r.registry.Get(name)
		if err != nil || conn == nil {
			continue
		}
		if err := conn.SubmitPromptForUserChoice(ctx, cd); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("prompt_for_user_choice router: no connector accepted the verdict")
	}
	return lastErr
}

// buildFeishuOutboundWorker constructs the outbound worker for DB-driven
// workspace connectors and optional legacy env bots.
//
// Env contract:
//   - PARSAR_FEISHU_OUTBOUND_POLL_SECONDS   (optional; default 10, cap 600)
//   - PARSAR_FEISHU_OUTBOUND_BATCH_LIMIT    (optional; default 32, cap 256)
//   - PARSAR_FEISHU_OPENAPI_BASE_URL        (optional; default Feishu prod)
//   - PARSAR_FEISHU_DEFAULT_BOT_APP_ID      (optional; falls back to PARSAR_FEISHU_APP_ID)
//   - PARSAR_FEISHU_DEFAULT_BOT_APP_SECRET  (optional; falls back to PARSAR_FEISHU_APP_SECRET)
//   - PARSAR_MASTER_KEY                     (REQUIRED when enabled — same
//     key used by the secret vault encryptor)
func buildFeishuOutboundWorker(env func(string) string, dbStore *store.Store, connectorReg *connector.Registry, deviceResolver inflight.DeviceResolver, publicURL string) (*inflight.Worker, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return nil, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("feishu outbound: init secrets service: %w", err)
	}

	defaultBot := defaultFeishuSharedBotEnv(env)
	pollEvery := 10 * time.Second
	if raw := strings.TrimSpace(env("PARSAR_FEISHU_OUTBOUND_POLL_SECONDS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			log.Bg().Warn("PARSAR_FEISHU_OUTBOUND_POLL_SECONDS invalid; using default",
				"value", raw, "default_s", 10)
		} else if n > 600 {
			log.Bg().Warn("PARSAR_FEISHU_OUTBOUND_POLL_SECONDS too high; capping",
				"value", n, "cap_s", 600)
			pollEvery = 600 * time.Second
		} else {
			pollEvery = time.Duration(n) * time.Second
		}
	}

	// PARSAR_FEISHU_OUTBOUND_BATCH_LIMIT is no longer honoured;
	// inflight driver uses a built-in batch limit. Boot still parses
	// + warns about misconfig so operators learn the knob is gone.
	if raw := strings.TrimSpace(env("PARSAR_FEISHU_OUTBOUND_BATCH_LIMIT")); raw != "" {
		log.Bg().Warn("PARSAR_FEISHU_OUTBOUND_BATCH_LIMIT is no longer honoured; inflight driver uses a built-in batch limit",
			"value", raw)
	}

	outboundChannels, err := buildOutboundChannels(env, dbStore)
	if err != nil {
		return nil, fmt.Errorf("feishu outbound: build outbound channels: %w", err)
	}

	worker, err := inflight.NewWorker(inflight.Options{
		Store:        dbStore,
		Secrets:      secretsSvc,
		Logger:       slogWorkerLogger{},
		PollInterval: pollEvery,
		BaseURL:      strings.TrimSpace(env("PARSAR_FEISHU_OPENAPI_BASE_URL")),
		PublicURL:    publicURL,
		DefaultSharedBot: inflight.DefaultSharedBotConfig{
			AppID:     defaultBot.appID,
			AppSecret: defaultBot.appSecret,
		},
		PermissionRouter: outboundPermissionRouter{registry: connectorReg},
		DeviceResolver:   deviceResolver,
		Channels:         outboundChannels,
	})
	if err != nil {
		return nil, fmt.Errorf("feishu outbound: init worker: %w", err)
	}
	log.Bg().Info("feishu outbound worker configured",
		"poll_interval", pollEvery,
		"default_shared_bot", defaultBot.configured(),
		"permission_router", connectorReg != nil,
	)
	return worker, nil
}

// slackBotSecretLookupAdapter bridges *store.Store to the slack channel's
// SlackBotSecretLookup seam: the store returns store.SlackBotSecret, the
// channel consumes its own package-local shape (so the channel never imports
// internal/store). A thin field copy is all that differs.
type slackBotSecretLookupAdapter struct{ store *store.Store }

func (a slackBotSecretLookupAdapter) ResolveSlackBotSecretByTeam(ctx context.Context, teamID string) (slackchannel.SlackBotSecret, error) {
	sec, err := a.store.ResolveSlackBotSecretByTeam(ctx, teamID)
	if err != nil {
		return slackchannel.SlackBotSecret{}, err
	}
	return slackchannel.SlackBotSecret{AppID: sec.AppID, EncryptedPayload: sec.EncryptedPayload}, nil
}

// buildSlackCredentialResolver assembles the per-team Slack bot-token resolver.
// When a master key (the secret-vault seal) and a store are available it returns
// a DB-backed resolver keyed by Slack team_id, falling back to the env token
// (PARSAR_SLACK_BOT_TOKEN) when a workspace has no kind='slack_bot' secret —
// the Hermes primary-fallback shape. Without a master key it returns nil so the
// caller keeps the default static/env resolver (Config-built). The env token is
// always allowed to be empty: a DB-only deployment resolves every workspace from
// secrets.
func buildSlackCredentialResolver(env func(string) string, dbStore *store.Store) (channel.CredentialResolver, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" || dbStore == nil {
		return nil, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("slack credential resolver: init secrets service: %w", err)
	}
	fallback := slackchannel.NewStaticCredentialResolver(
		strings.TrimSpace(env("PARSAR_SLACK_APP_ID")),
		strings.TrimSpace(env("PARSAR_SLACK_BOT_TOKEN")),
	)
	legacy := slackchannel.NewDBCredentialResolver(
		slackBotSecretLookupAdapter{store: dbStore},
		secretsSvc,
		fallback,
	)
	// Layer the workspace-dimension (app_id) resolver in front so a connector
	// configured via the admin panel resolves first; legacy team_id/env is the
	// fallback.
	return wrapWithWorkspaceConnectorResolver(env, dbStore, "slack", legacy)
}

// discordBotSecretLookupAdapter bridges *store.Store to the discord channel's
// DiscordBotSecretLookup seam: the store returns store.DiscordBotSecret, the
// channel consumes its own package-local shape (so the channel never imports
// internal/store). A thin field copy is all that differs — the Discord twin of
// slackBotSecretLookupAdapter, keyed by guild_id instead of team_id.
type discordBotSecretLookupAdapter struct{ store *store.Store }

func (a discordBotSecretLookupAdapter) ResolveDiscordBotSecretByGuild(ctx context.Context, guildID string) (discordchannel.DiscordBotSecret, error) {
	sec, err := a.store.ResolveDiscordBotSecretByGuild(ctx, guildID)
	if err != nil {
		return discordchannel.DiscordBotSecret{}, err
	}
	return discordchannel.DiscordBotSecret{AppID: sec.AppID, EncryptedPayload: sec.EncryptedPayload}, nil
}

// buildDiscordCredentialResolver assembles the per-guild Discord bot-token
// resolver. When a master key (the secret-vault seal) and a store are available
// it returns a DB-backed resolver keyed by Discord guild_id, falling back to the
// env token (PARSAR_DISCORD_BOT_TOKEN) when a guild has no kind='discord_bot'
// secret — the same primary-fallback shape Slack uses. Without a master key it
// returns nil so the caller keeps the default static/env resolver. One Discord
// bot connects with one token across all its guilds, so the DB-per-guild path
// chiefly supports running several distinct bots from one process.
func buildDiscordCredentialResolver(env func(string) string, dbStore *store.Store) (channel.CredentialResolver, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" || dbStore == nil {
		return nil, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("discord credential resolver: init secrets service: %w", err)
	}
	fallback := discordchannel.NewStaticCredentialResolver(
		strings.TrimSpace(env("PARSAR_DISCORD_APP_ID")),
		strings.TrimSpace(env("PARSAR_DISCORD_BOT_TOKEN")),
	)
	legacy := discordchannel.NewDBCredentialResolver(
		discordBotSecretLookupAdapter{store: dbStore},
		secretsSvc,
		fallback,
	)
	return wrapWithWorkspaceConnectorResolver(env, dbStore, "discord", legacy)
}

// workspaceConnectorResolver is the workspace-dimension bot-token resolver.
// It keys on the platform app_id (the join key into workspace_im_connectors
// that is known at config-save time, unlike team_id/guild_id), reads the
// connector's config.bot_token_ref, fetches that vault secret and decrypts
// it per call so a rotated token takes effect without a restart. Any miss
// (empty app_id, no enabled connector, missing ref) falls through to the
// legacy team_id/guild_id (+ env) resolver, so existing deployments keep
// working unchanged.
type workspaceConnectorResolver struct {
	store    *store.Store
	secrets  *secrets.Service
	platform string // "slack" | "discord" | "teams"
	tokenRef string // config key holding the secret id (e.g. "bot_token_ref")
	// extract reads the credential value out of the decrypted payload. Nil
	// defaults to botTokenFromPayload (Slack/Discord bot tokens); Teams injects
	// appPasswordFromPayload since its secret is keyed "app_password".
	extract  func(map[string]any) string
	fallback channel.CredentialResolver
}

func (r *workspaceConnectorResolver) Resolve(ctx context.Context, botID string) (channel.Credential, error) {
	appID := strings.TrimSpace(botID)
	if appID == "" {
		return r.fallbackResolve(ctx, botID)
	}
	conn, err := r.store.GetWorkspaceConnectorByAppID(ctx, r.platform, appID)
	if err != nil {
		// botID may be a legacy team_id/guild_id rather than an app_id, or no
		// workspace connector exists yet — defer to the legacy resolver.
		return r.fallbackResolve(ctx, botID)
	}
	secretID, _ := conn.Config[r.tokenRef].(string)
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return r.fallbackResolve(ctx, botID)
	}
	payload, err := r.store.GetSecretPayload(ctx, conn.WorkspaceID, secretID)
	if err != nil {
		return r.fallbackResolve(ctx, botID)
	}
	decrypted, err := r.secrets.Decrypt(payload.EncryptedPayload)
	if err != nil {
		return channel.Credential{}, fmt.Errorf("%s channel: decrypt bot token for app_id %s: %w", r.platform, appID, err)
	}
	extract := r.extract
	if extract == nil {
		extract = botTokenFromPayload
	}
	token := strings.TrimSpace(extract(decrypted))
	if token == "" {
		return channel.Credential{}, fmt.Errorf("%s channel: connector secret for app_id %s has no token value", r.platform, appID)
	}
	return channel.Credential{AppID: appID, AppSecret: token}, nil
}

func (r *workspaceConnectorResolver) fallbackResolve(ctx context.Context, botID string) (channel.Credential, error) {
	if r.fallback == nil {
		return channel.Credential{}, fmt.Errorf("%s channel: no workspace connector for app_id %q and no fallback resolver", r.platform, strings.TrimSpace(botID))
	}
	return r.fallback.Resolve(ctx, botID)
}

// botTokenFromPayload reads a bot token out of a decrypted secret payload
// using the same key precedence the rest of the codebase uses for shared
// credentials (api_key → token → access_token → value).
func botTokenFromPayload(payload map[string]any) string {
	for _, key := range []string{"bot_token", "api_key", "token", "access_token", "value"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// wrapWithWorkspaceConnectorResolver layers the workspace-dimension (app_id)
// resolver in front of an existing platform resolver. Returns the input
// unchanged when no master key / store is available (resolver stays legacy).
func wrapWithWorkspaceConnectorResolver(env func(string) string, dbStore *store.Store, platform string, legacy channel.CredentialResolver) (channel.CredentialResolver, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" || dbStore == nil {
		return legacy, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("%s workspace connector resolver: init secrets service: %w", platform, err)
	}
	return &workspaceConnectorResolver{
		store:    dbStore,
		secrets:  secretsSvc,
		platform: platform,
		tokenRef: "bot_token_ref",
		fallback: legacy,
	}, nil
}

// appPasswordFromPayload reads a Teams AAD client secret out of a decrypted
// secret payload. It prefers the connector-specific "app_password" key, then
// falls back to the generic shared-credential keys so a secret minted by any of
// the standard forms still resolves.
func appPasswordFromPayload(payload map[string]any) string {
	for _, key := range []string{"app_password", "client_secret", "api_key", "token", "value"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// buildTeamsCredentialResolver assembles the outbound Teams credential resolver.
// With a master key + store it returns a workspace-dimension resolver keyed on
// the Microsoft App Id (config.app_password_ref → vault secret → app password),
// falling back to the env static (PARSAR_TEAMS_APP_ID/APP_PASSWORD) when a bot
// has no connector row. Without a master key it returns nil so the caller keeps
// the adapter's Config-derived static resolver.
func buildTeamsCredentialResolver(env func(string) string, dbStore *store.Store) (channel.CredentialResolver, error) {
	masterKey := strings.TrimSpace(env("PARSAR_MASTER_KEY"))
	if masterKey == "" || dbStore == nil {
		return nil, nil
	}
	secretsSvc, err := secrets.New(masterKey)
	if err != nil {
		return nil, fmt.Errorf("teams credential resolver: init secrets service: %w", err)
	}
	fallback := teamschannel.NewStaticCredentialResolver(
		strings.TrimSpace(env("PARSAR_TEAMS_APP_ID")),
		strings.TrimSpace(env("PARSAR_TEAMS_APP_PASSWORD")),
	)
	return &workspaceConnectorResolver{
		store:    dbStore,
		secrets:  secretsSvc,
		platform: "teams",
		tokenRef: "app_password_ref",
		extract:  appPasswordFromPayload,
		fallback: fallback,
	}, nil
}

// storeAllowsTeamsAppID builds the multi-tenant inbound-JWT audience predicate:
// an app_id is accepted when it matches the env bot id, or an enabled Teams
// connector row exists for it. GetWorkspaceConnectorByAppID returns only enabled
// rows, so a disabled connector's app_id is rejected — the token audience must
// name a bot this deployment actively serves.
func storeAllowsTeamsAppID(dbStore *store.Store, envAppID string) func(string) bool {
	envAppID = strings.TrimSpace(envAppID)
	return func(appID string) bool {
		appID = strings.TrimSpace(appID)
		if appID == "" {
			return false
		}
		if envAppID != "" && appID == envAppID {
			return true
		}
		if dbStore == nil {
			return false
		}
		if _, err := dbStore.GetWorkspaceConnectorByAppID(context.Background(), "teams", appID); err == nil {
			return true
		}
		return false
	}
}

// buildOutboundChannels assembles the neutral outbound channel registry the
// inflight worker dispatches non-Feishu terminal/progress cards through. Feishu
// is NOT registered here (the worker mints it per-conversation with its
// transport-injected token cache). A platform appears only when its env is
// fully configured, so an unconfigured deployment yields an empty map and the
// worker stays pure-Feishu with zero behavior change.
//
// Slack registers when the app token is configured (Slack is in play), even
// without an env bot token: a per-team DB resolver (kind='slack_bot' keyed by
// Slack team_id) mints the right xoxb token per call, with the env bot token as
// a single-tenant fallback. When no master key is available the registry gate
// keeps the legacy behavior — register only when the static env bot token is
// present. The adapter is a separate, stateless instance (it resolves its own
// token per call) so sharing the runner's instance is unnecessary.
func buildOutboundChannels(env func(string) string, dbStore *store.Store) (map[channel.Platform]channel.Channel, error) {
	channels := map[channel.Platform]channel.Channel{}
	teamsAppID := strings.TrimSpace(env("PARSAR_TEAMS_APP_ID"))
	teamsAppPassword := strings.TrimSpace(env("PARSAR_TEAMS_APP_PASSWORD"))
	teamsTenantID := strings.TrimSpace(env("PARSAR_TEAMS_TENANT_ID"))
	teamsWebhook := truthy(env("PARSAR_TEAMS_WEBHOOK")) || truthy(env("PARSAR_TEAMS_CONNECTORS"))
	teamsResolver, err := buildTeamsCredentialResolver(env, dbStore)
	if err != nil {
		return nil, err
	}
	// Teams registers for the inflight worker whenever the webhook/connectors
	// path is on and a credential is resolvable — either the env app
	// id+password or the workspace-dimension DB resolver (multi-bot). The
	// per-call resolver does both: env static under the hood, DB-fronted
	// when a master key + store are available.
	if teamsWebhook && (teamsResolver != nil || (teamsAppID != "" && teamsAppPassword != "")) {
		opts := []teamschannel.Option{}
		if teamsResolver != nil {
			opts = append(opts, teamschannel.WithCredentialResolver(teamsResolver))
		}
		channels[channel.PlatformTeams] = teamschannel.New(teamschannel.Config{
			AppID:       teamsAppID,
			AppPassword: teamsAppPassword,
			TenantID:    teamsTenantID,
		}, opts...)
		log.Bg().Info("teams outbound channel registered for inflight worker",
			"workspace_resolver", teamsResolver != nil,
			"env_bot", teamsAppID != "",
			"tenant_pinned", teamsTenantID != "")
	}
	slackBot := strings.TrimSpace(env("PARSAR_SLACK_BOT_TOKEN"))
	slackApp := strings.TrimSpace(env("PARSAR_SLACK_APP_TOKEN"))
	dbResolver, err := buildSlackCredentialResolver(env, dbStore)
	if err != nil {
		return nil, err
	}
	// Register when we can resolve a bot token for at least one workspace:
	// either the workspace/per-team DB resolver or the static env bot token.
	// The outbound path only calls chat.postMessage, which needs the bot token
	// (xoxb) — not the app-level Socket Mode token — so PARSAR_SLACK_APP_TOKEN
	// is irrelevant here and must not gate registration (connector-mode has no
	// env app token yet still replies via the SourceAppID-keyed DB resolver).
	if dbResolver != nil || slackBot != "" {
		opts := []slackchannel.Option{}
		if dbResolver != nil {
			opts = append(opts, slackchannel.WithCredentialResolver(dbResolver))
		}
		channels[channel.PlatformSlack] = slackchannel.New(slackchannel.Config{
			AppID:    strings.TrimSpace(env("PARSAR_SLACK_APP_ID")),
			BotToken: slackBot,
			AppToken: slackApp,
		}, opts...)
		log.Bg().Info("slack outbound channel registered for inflight worker",
			"per_team_resolver", dbResolver != nil,
			"static_token", slackBot != "")
	}
	// Discord registers when a bot token is configured (single-tenant fallback) or
	// a per-guild DB resolver is available — the Slack shape, keyed by guild_id.
	// Unlike Slack there is no separate app-level socket token: the bot token is
	// the only gate.
	discordBot := strings.TrimSpace(env("PARSAR_DISCORD_BOT_TOKEN"))
	discordResolver, err := buildDiscordCredentialResolver(env, dbStore)
	if err != nil {
		return nil, err
	}
	if discordResolver != nil || discordBot != "" {
		opts := []discordchannel.Option{}
		if discordResolver != nil {
			opts = append(opts, discordchannel.WithCredentialResolver(discordResolver))
		}
		channels[channel.PlatformDiscord] = discordchannel.New(discordchannel.Config{
			AppID:    strings.TrimSpace(env("PARSAR_DISCORD_APP_ID")),
			BotToken: discordBot,
		}, opts...)
		log.Bg().Info("discord outbound channel registered for inflight worker",
			"per_guild_resolver", discordResolver != nil,
			"static_token", discordBot != "")
	}
	return channels, nil
}

// outboundPermissionRouter adapts the connector registry to
// inflight.PermissionRouter. Kept distinct from
// inboundPermissionRouter because inbound and inflight
// deliberately don't share a PermissionRouter type.
type outboundPermissionRouter struct {
	registry *connector.Registry
}

func (r outboundPermissionRouter) SubmitPermission(ctx context.Context, decision inflight.PermissionDecision) error {
	if r.registry == nil {
		return errors.New("permission router: connector registry not configured")
	}
	cd := connector.PermissionDecision{
		RequestID: decision.RequestID,
		Approved:  decision.Approved,
		Note:      decision.Note,
		By:        decision.OperatorID,
	}
	var lastErr error
	for _, name := range r.registry.Types() {
		conn, err := r.registry.Get(name)
		if err != nil || conn == nil {
			continue
		}
		if err := conn.SubmitPermission(ctx, cd); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("permission router: no connector accepted the verdict")
	}
	return lastErr
}

// truthy interprets common "this flag is on" env values. "" / "0" /
// "false" return false; "1" / "true" / "yes" return true (case-insensitive).
func truthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// slogFeishuInboundLogger adapts log/slog to the inbound.Logger interface.
type slogFeishuInboundLogger struct{}

func (slogFeishuInboundLogger) Info(msg string, args ...any) {
	log.Bg().Info("feishu inbound: "+msg, args...)
}
func (slogFeishuInboundLogger) Warn(msg string, args ...any) {
	log.Bg().Warn("feishu inbound: "+msg, args...)
}

// slogWorkerLogger adapts log/slog to the inflight.Logger interface
// without coupling the package to slog. Keeps the args pattern intact
// so structured-log consumers still see key/value pairs.
type slogWorkerLogger struct{}

func (slogWorkerLogger) Info(msg string, args ...any) {
	log.Bg().Info("feishu outbound: "+msg, args...)
}
func (slogWorkerLogger) Warn(msg string, args ...any) {
	log.Bg().Warn("feishu outbound: "+msg, args...)
}

// streamingDispatcherAdapter bridges store.StreamingDispatcher to
// dev.StartConversationRun. `store` and `dev` don't import each other;
// main is the only place both arrive together.
//
// Fire-and-forget per the interface contract: failures surface to the
// user via the SSE /stream error event + agent_runs.status=failed.
// We use slog (not the audit ingester) because these errors point at
// concrete wiring/connectivity issues operators need to see in stdout.
type streamingDispatcherAdapter struct {
	runtimeStore dev.RuntimeStore
	deps         dev.StreamingDispatchDeps
	failRun      func(ctx context.Context, runID, source, reason string)
}

func (a streamingDispatcherAdapter) Start(ctx context.Context, in store.StreamingDispatchInput) {
	_, err := dev.StartConversationRun(ctx, a.runtimeStore, a.deps, in.RunID, in.ConversationID)
	if err == nil {
		return
	}
	log.Bg().Error("streaming dispatcher: auto-start failed",
		"run_id", in.RunID,
		"conversation_id", in.ConversationID,
		"connector_type", in.ConnectorType,
		"error", err)
	if a.failRun != nil {
		a.failRun(ctx, in.RunID, in.ConnectorType, "auto-start failed: "+err.Error())
	}
}

// blobDownloadAdapter exposes blob.Store on the agent-daemon connector's
// narrow OSSPresigner interface (PresignGet → short-lived GET URL). The
// daemon discards the expiry; we still return it for signature parity.
type blobDownloadAdapter struct{ store blob.Store }

func (a blobDownloadAdapter) PresignGet(ctx context.Context, ref string, ttl time.Duration) (string, time.Time, error) {
	spec, err := a.store.DownloadURL(ctx, ref, ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	return spec.URL, spec.Expires, nil
}
