use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use anyhow::{Context, Result, anyhow, ensure};
use codex_app_server::in_process::{
    self, InProcessClientSender, InProcessServerEvent, InProcessStartArgs,
};
use codex_app_server_protocol::{
    ClientRequest, InitializeParams, JSONRPCErrorError, ServerNotification, ThreadItem,
};
use codex_arg0::Arg0DispatchPaths;
use codex_config::{CloudConfigBundleLoader, LoaderOverrides, NoopThreadConfigLoader};
use codex_core::config::{ConfigBuilder, ConfigOverrides};
use codex_exec_server::{EnvironmentManager, ExecServerRuntimePaths};
use codex_feedback::CodexFeedback;
use codex_protocol::protocol::SessionSource;
use codex_shell_command::parse_command::extract_shell_command;
use serde_json::{Value, json};
use tokio::sync::oneshot;
use tokio::task::JoinHandle;
use tokio::time::{sleep, timeout};

pub async fn configuration(
    root: &Path,
    binary: PathBuf,
) -> Result<(InProcessStartArgs, Arc<EnvironmentManager>)> {
    let home = root.join("harness/codex");
    tokio::fs::create_dir_all(&home)
        .await
        .context("create native history directory")?;
    let mut overrides: Vec<(String, toml::Value)> = vec![
        ("model", "MiniMax-M3"),
        ("model_provider", "shared_files"),
        ("approval_policy", "never"),
        ("sandbox_mode", "danger-full-access"),
        ("web_search", "disabled"),
        ("shell_environment_policy.inherit", "core"),
        (
            "model_providers.shared_files.name",
            "Shared native files acceptance",
        ),
        (
            "model_providers.shared_files.base_url",
            "https://api.minimax.cn/v1",
        ),
        (
            "model_providers.shared_files.env_key",
            "PARSAR_PROBE_MODEL_KEY",
        ),
        ("model_providers.shared_files.wire_api", "responses"),
    ]
    .into_iter()
    .map(|(key, value)| (key.to_owned(), toml::Value::String(value.to_owned())))
    .collect();
    overrides.push((
        "features.multi_agent".to_owned(),
        toml::Value::Boolean(false),
    ));
    overrides.push((
        "shell_environment_policy.ignore_default_excludes".to_owned(),
        toml::Value::Boolean(false),
    ));
    overrides.push((
        "shell_environment_policy.exclude".to_owned(),
        toml::Value::Array(vec![
            toml::Value::String("PARSAR_PLACEMENT_MODEL_KEY_FILE".to_owned()),
            toml::Value::String("PARSAR_PROBE_MODEL_KEY".to_owned()),
            toml::Value::String("CODEX_EXEC_SERVER_NOISE_*".to_owned()),
        ]),
    ));
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

#[derive(Clone, Default)]
struct Observed {
    failure: Option<&'static str>,
    turns_started: Vec<(String, String)>,
    turns_completed: Vec<(String, String, Value)>,
    commands_started: Vec<(String, String, String)>,
    commands_completed: Vec<(String, String, Value)>,
    answer: String,
    events: Vec<Value>,
    event_bytes: usize,
    event_count: usize,
}

impl Observed {
    fn record(&mut self, notification: ServerNotification) -> Result<()> {
        self.event_count += 1;
        ensure!(
            self.event_count <= 10_000,
            "native event count exceeded fixture bound"
        );
        let relevant = match &notification {
            ServerNotification::TurnStarted(event) => {
                self.turns_started
                    .push((event.thread_id.clone(), event.turn.id.clone()));
                true
            }
            ServerNotification::TurnCompleted(event) => {
                self.turns_completed.push((
                    event.thread_id.clone(),
                    event.turn.id.clone(),
                    serde_json::to_value(&event.turn)?,
                ));
                true
            }
            ServerNotification::ItemStarted(event) => {
                if let ThreadItem::CommandExecution { id, .. } = &event.item {
                    self.commands_started.push((
                        event.thread_id.clone(),
                        event.turn_id.clone(),
                        id.clone(),
                    ));
                    true
                } else {
                    false
                }
            }
            ServerNotification::ItemCompleted(event) => match &event.item {
                ThreadItem::CommandExecution { .. } => {
                    self.commands_completed.push((
                        event.thread_id.clone(),
                        event.turn_id.clone(),
                        serde_json::to_value(&event.item)?,
                    ));
                    true
                }
                ThreadItem::AgentMessage { text, phase, .. } => {
                    let phase = serde_json::to_value(phase)?;
                    if phase.is_null() || phase == "final_answer" {
                        ensure!(
                            self.answer.len() + text.len() <= 256 * 1024,
                            "answer exceeded fixture bound"
                        );
                        self.answer.push_str(text);
                        self.answer.push('\n');
                    }
                    true
                }
                _ => false,
            },
            _ => false,
        };
        ensure!(
            self.turns_started.len() <= 1
                && self.turns_completed.len() <= 1
                && self.commands_started.len() <= 1
                && self.commands_completed.len() <= 1,
            "unexpected additional Turn or command"
        );
        if relevant {
            let value = serde_json::to_value(notification)?;
            self.event_bytes += serde_json::to_vec(&value)?.len();
            ensure!(
                self.event_bytes <= 4 * 1024 * 1024,
                "native observations exceeded fixture byte bound"
            );
            self.events.push(value);
        }
        Ok(())
    }
}

pub struct Evidence {
    pub answer: String,
    pub command: Value,
    pub events: Vec<Value>,
}

pub struct Runtime {
    sender: InProcessClientSender,
    observed: Arc<Mutex<Observed>>,
    stop: oneshot::Sender<()>,
    drain: JoinHandle<Result<()>>,
}

impl Runtime {
    pub async fn start(args: InProcessStartArgs) -> Result<Self> {
        let mut handle = timeout(Duration::from_secs(45), in_process::start(args))
            .await
            .context("embedded app-server startup timed out")?
            .context("embedded app-server startup failed")?;
        let sender = handle.sender();
        let observed = Arc::new(Mutex::new(Observed::default()));
        let state = observed.clone();
        let (stop, mut stopped) = oneshot::channel();
        let drain = tokio::spawn(async move {
            loop {
                tokio::select! {
                    _ = &mut stopped => break,
                    event = handle.next_event() => {
                        match event {
                            Some(InProcessServerEvent::ServerNotification(notification)) => {
                                let mut state = state.lock().map_err(|_| anyhow!("observation lock poisoned"))?;
                                if state.failure.is_none() && state.record(*notification).is_err() {
                                    state.failure = Some("native observation bounds or counts failed");
                                }
                            }
                            Some(InProcessServerEvent::ServerRequest(request)) => {
                                let _ = handle.fail_server_request(request.id().clone(), JSONRPCErrorError { code: -32601, message: "Unexpected client request in shared files probe".to_owned(), data: None });
                                state.lock().map_err(|_| anyhow!("observation lock poisoned"))?.failure = Some("unexpected native client request");
                            }
                            Some(InProcessServerEvent::Lagged { .. }) => {
                                state.lock().map_err(|_| anyhow!("observation lock poisoned"))?.failure = Some("native Lagged event observed");
                            }
                            None => {
                                state.lock().map_err(|_| anyhow!("observation lock poisoned"))?.failure = Some("native event stream closed early");
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
        Ok(Self {
            sender,
            observed,
            stop,
            drain,
        })
    }

    pub async fn request(&self, id: i64, method: &str, params: Value) -> Result<Value> {
        self.healthy()?;
        let request: ClientRequest =
            serde_json::from_value(json!({"id":id,"method":method,"params":params}))
                .context("native typed request parameters failed")?;
        timeout(Duration::from_secs(60), self.sender.request(request))
            .await
            .with_context(|| format!("native {method} timed out"))?
            .with_context(|| format!("native {method} transport failed"))?
            .map_err(|_| anyhow!("native {method} request failed"))
    }

    fn snapshot(&self) -> Result<Observed> {
        Ok(self
            .observed
            .lock()
            .map_err(|_| anyhow!("observation lock poisoned"))?
            .clone())
    }

    pub fn healthy(&self) -> Result<()> {
        let state = self
            .observed
            .lock()
            .map_err(|_| anyhow!("observation lock poisoned"))?;
        ensure!(
            state.failure.is_none(),
            "{}",
            state.failure.unwrap_or("native observation failed")
        );
        Ok(())
    }

    pub fn require_active(&self, thread: &str, turn: &str) -> Result<()> {
        self.healthy()?;
        let state = self.snapshot()?;
        ensure!(
            state.turns_started == vec![(thread.to_owned(), turn.to_owned())]
                && state.turns_completed.is_empty(),
            "native Turn is not observed active"
        );
        ensure!(
            state.commands_started.len() == 1 && state.commands_completed.is_empty(),
            "native command is not observed active"
        );
        ensure!(
            state.commands_started[0].0 == thread && state.commands_started[0].1 == turn,
            "active command belongs to another Turn"
        );
        Ok(())
    }

    pub fn require_unfinished(&self) -> Result<()> {
        self.healthy()?;
        ensure!(
            self.snapshot()?.turns_completed.is_empty(),
            "native Turn completed before command heartbeat"
        );
        Ok(())
    }

    pub async fn wait_active(&self, thread: &str, turn: &str) -> Result<()> {
        loop {
            self.healthy()?;
            let state = self.snapshot()?;
            ensure!(
                state.turns_completed.is_empty() && state.commands_completed.is_empty(),
                "command completed before active checkpoint"
            );
            if state.turns_started.len() == 1 && state.commands_started.len() == 1 {
                return self.require_active(thread, turn);
            }
            sleep(Duration::from_millis(25)).await;
        }
    }

    pub async fn wait_completed(&self) -> Result<()> {
        loop {
            self.healthy()?;
            if !self.snapshot()?.turns_completed.is_empty() {
                return Ok(());
            }
            sleep(Duration::from_millis(100)).await;
        }
    }

    pub fn validate(
        &self,
        thread: &str,
        turn: &str,
        phase: &str,
        workspace: &Path,
        marker: &str,
        history: &str,
    ) -> Result<Evidence> {
        self.healthy()?;
        let state = self.snapshot()?;
        ensure!(
            state.turns_started == vec![(thread.to_owned(), turn.to_owned())]
                && state.turns_completed.len() == 1,
            "native Turn lifecycle missing"
        );
        let completed = &state.turns_completed[0];
        ensure!(
            completed.0 == thread && completed.1 == turn && completed.2["status"] == "completed",
            "native Turn did not complete successfully"
        );
        ensure!(
            state.commands_started.len() == 1 && state.commands_completed.len() == 1,
            "native command lifecycle missing"
        );
        let started = &state.commands_started[0];
        let completed = &state.commands_completed[0];
        let item = &completed.2;
        ensure!(
            started.0 == thread
                && started.1 == turn
                && completed.0 == thread
                && completed.1 == turn
                && item["id"] == started.2,
            "command lifecycle identities differ"
        );
        let command = item["command"]
            .as_str()
            .context("native command text missing")?;
        ensure!(
            is_gate_command(command, phase),
            "model executed an unexpected command"
        );
        let cwd = workspace.to_str().context("workspace encoding")?;
        ensure!(
            item["cwd"] == cwd && item["exitCode"] == 7,
            "native command cwd or exit differs"
        );
        let output = item["aggregatedOutput"]
            .as_str()
            .context("native command output missing")?;
        ensure!(
            output.contains(marker)
                && output.contains(&format!("remote-stdout:{phase}"))
                && output.contains(&format!("remote-stderr:{phase}")),
            "native command output observations missing"
        );
        ensure!(
            !command.contains(history) && !output.contains(history),
            "prompt-only history leaked into command or files"
        );
        ensure!(
            state.answer.contains(marker) && state.answer.contains(history),
            "native final answer did not recall marker and history"
        );
        Ok(Evidence {
            answer: state.answer,
            command: json!({"id":item["id"],"command":command,"cwd":cwd,"aggregated_output":output,"exit_code":7,"started":true,"completed":true}),
            events: state.events,
        })
    }

    pub async fn shutdown(self) -> Result<()> {
        let observed = self.observed.clone();
        let _ = self.stop.send(());
        timeout(Duration::from_secs(50), self.drain)
            .await
            .context("embedded shutdown exceeded fixture deadline")?
            .context("event drain task failed")??;
        let state = observed
            .lock()
            .map_err(|_| anyhow!("observation lock poisoned"))?;
        ensure!(
            state.failure.is_none(),
            "{}",
            state.failure.unwrap_or("native observation failed")
        );
        Ok(())
    }
}

fn is_gate_command(command: &str, phase: &str) -> bool {
    let Some(argv) = shlex::split(command) else {
        return false;
    };
    argv == ["./shared-gate.sh", phase]
        || extract_shell_command(&argv)
            .is_some_and(|(_, script)| script == format!("./shared-gate.sh {phase}"))
}

#[cfg(test)]
mod tests {
    use super::is_gate_command;

    #[test]
    fn exact_gate_accepts_native_presentation_only() {
        assert!(is_gate_command("./shared-gate.sh first", "first"));
        assert!(is_gate_command(
            "/bin/bash -lc './shared-gate.sh first'",
            "first"
        ));
        assert!(!is_gate_command(
            "/bin/bash -lc './shared-gate.sh first; echo invented'",
            "first"
        ));
        assert!(!is_gate_command("./shared-gate.sh fresh", "first"));
        assert!(!is_gate_command(
            "./shared-gate.sh first && echo invented",
            "first"
        ));
    }
}
