use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result, anyhow};
use codex_app_server::in_process::{
    self, InProcessClientSender, InProcessServerEvent, InProcessStartArgs,
};
use codex_app_server_protocol::{ClientRequest, InitializeParams, JSONRPCErrorError};
use codex_arg0::Arg0DispatchPaths;
use codex_config::{CloudConfigBundleLoader, LoaderOverrides, NoopThreadConfigLoader};
use codex_core::config::{ConfigBuilder, ConfigOverrides};
use codex_exec_server::{EnvironmentManager, ExecServerRuntimePaths};
use codex_feedback::CodexFeedback;
use codex_protocol::protocol::SessionSource;
use serde_json::{Value, json};
use tokio::sync::oneshot;
use tokio::task::JoinHandle;
use tokio::time::timeout;

use super::observations::Observations;

async fn configuration(
    root: &Path,
    binary: PathBuf,
) -> Result<(InProcessStartArgs, Arc<EnvironmentManager>)> {
    let home = root.join("harness/codex");
    tokio::fs::create_dir_all(&home)
        .await
        .context("create native history directory")?;
    let overrides = super::configuration::overrides();
    let loader = LoaderOverrides {
        ignore_user_config: true,
        ignore_project_config: true,
        ..LoaderOverrides::default()
    };
    let paths = Arg0DispatchPaths {
        codex_self_exe: Some(binary.clone()),
        ..Arg0DispatchPaths::default()
    };
    let config = Arc::new(
        ConfigBuilder::default()
            .codex_home(home)
            .cli_overrides(overrides.clone())
            .loader_overrides(loader.clone())
            .harness_overrides(ConfigOverrides {
                cwd: Some(root.join("harness")),
                codex_self_exe: Some(binary.clone()),
                ..ConfigOverrides::default()
            })
            .build()
            .await
            .context("native configuration failed")?,
    );
    let manager = Arc::new(
        EnvironmentManager::from_env(
            Some(
                ExecServerRuntimePaths::new(binary, None)
                    .context("native resource paths failed")?,
            ),
            config.http_client_factory(),
        )
        .await
        .context("native Environment manager failed")?,
    );
    let state_db = codex_rollout::state_db::try_init(config.as_ref())
        .await
        .context("native history initialization failed")?;
    let initialize: InitializeParams = serde_json::from_value(json!({"clientInfo":{"name":"parsar_shared_files_probe","version":"1"},"capabilities":{"experimentalApi":true}})).context("initialize parameters failed")?;
    Ok((
        InProcessStartArgs {
            arg0_paths: paths,
            config,
            cli_overrides: overrides,
            loader_overrides: loader,
            strict_config: false,
            cloud_config_bundle: CloudConfigBundleLoader::default(),
            thread_config_loader: Arc::new(NoopThreadConfigLoader),
            feedback: CodexFeedback::new(),
            log_db: None,
            state_db: Some(state_db),
            environment_manager: manager.clone(),
            config_warnings: Vec::new(),
            session_source: SessionSource::Exec,
            enable_codex_api_key_env: false,
            initialize,
            channel_capacity: 1024,
        },
        manager,
    ))
}

pub struct Runtime {
    sender: InProcessClientSender,
    pub observations: Observations,
    stop: oneshot::Sender<()>,
    drain: JoinHandle<Result<()>>,
}

impl Runtime {
    pub async fn start(root: &Path, binary: PathBuf) -> Result<(Self, Arc<EnvironmentManager>)> {
        let (args, manager) = configuration(root, binary).await?;
        let mut handle = timeout(Duration::from_secs(45), in_process::start(args))
            .await
            .context("embedded app-server startup timed out")?
            .context("embedded app-server startup failed")?;
        let sender = handle.sender();
        let observations = Observations::default();
        let state = observations.clone();
        let (stop, mut stopped) = oneshot::channel();
        let drain = tokio::spawn(async move {
            loop {
                tokio::select! {
                    _ = &mut stopped => break,
                    event = handle.next_event() => {
                        match event {
                            Some(InProcessServerEvent::ServerNotification(notification)) => {
                                state.record(*notification)?;
                            }
                            Some(InProcessServerEvent::ServerRequest(request)) => {
                                let _ = handle.fail_server_request(request.id().clone(), JSONRPCErrorError { code: -32601, message: "Unexpected client request in shared files probe".to_owned(), data: None });
                                state.fail("unexpected native client request")?;
                            }
                            Some(InProcessServerEvent::Lagged { .. }) => {
                                state.fail("native Lagged event observed")?;
                            }
                            None => {
                                state.fail("native event stream closed early")?;
                                break;
                            }
                        }
                    }
                }
            }
            handle
                .shutdown()
                .await
                .context("embedded app-server shutdown failed")
        });
        Ok((
            Self {
                sender,
                observations,
                stop,
                drain,
            },
            manager,
        ))
    }

    pub async fn request(&self, id: i64, method: &str, params: Value) -> Result<Value> {
        self.observations.healthy()?;
        let request: ClientRequest =
            serde_json::from_value(json!({"id":id,"method":method,"params":params}))
                .context("native typed request parameters failed")?;
        timeout(Duration::from_secs(60), self.sender.request(request))
            .await
            .with_context(|| format!("native {method} timed out"))?
            .with_context(|| format!("native {method} transport failed"))?
            .map_err(|_| anyhow!("native {method} request failed"))
    }

    pub async fn shutdown(self) -> Result<()> {
        let observed = self.observations.clone();
        let _ = self.stop.send(());
        timeout(Duration::from_secs(50), self.drain)
            .await
            .context("embedded shutdown exceeded fixture deadline")?
            .context("event drain task failed")??;
        observed.healthy()
    }
}

pub fn annotate(proof: &mut Value) {
    proof["status"] = json!("shared_native_files_characterized_with_blockers");
    proof["limitations"] = json!([
        "Pinned in-process queues can silently drop non-required notifications; only this bounded workflow's required observations were checked. Production lossless delivery remains blocked.",
        "This is typed in-process embedding, not raw stdio compatibility or a production daemon integration.",
        "Upstream shutdown may abort internal tasks; returned shutdown is not proof of executor OS quiescence.",
        "Direct native filesystem checks do not establish authorization, workspace confinement, public pagination or file_id semantics."
    ]);
}
