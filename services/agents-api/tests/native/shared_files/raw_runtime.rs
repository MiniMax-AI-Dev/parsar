use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result, anyhow, bail, ensure};
use codex_app_server::{
    AppServerRuntimeOptions, AppServerTransport, AppServerWebsocketAuthSettings,
    PluginStartupTasks, RemoteControlStartupMode,
    run_main_with_transport_options_and_environment_manager,
};
use codex_app_server_client::{
    AppServerEvent, AppServerRequestHandle, RemoteAppServerClient, RemoteAppServerConnectArgs,
    RemoteAppServerEndpoint,
};
use codex_app_server_protocol::{ClientRequest, JSONRPCErrorError};
use codex_arg0::Arg0DispatchPaths;
use codex_config::LoaderOverrides;
use codex_exec_server::EnvironmentManager;
use codex_protocol::protocol::SessionSource;
use codex_utils_absolute_path::AbsolutePathBuf;
use codex_utils_cli::CliConfigOverrides;
use serde_json::{Value, json};
use tempfile::TempDir;
use tokio::sync::oneshot;
use tokio::task::JoinHandle;
use tokio::time::{sleep, timeout};

use super::{configuration, observations::Observations};

pub struct Runtime {
    sender: AppServerRequestHandle,
    pub observations: Observations,
    stop: oneshot::Sender<()>,
    drain: JoinHandle<Result<()>>,
    runner: JoinHandle<Result<()>>,
    socket_directory: TempDir,
}

impl Runtime {
    pub async fn start(root: &Path, binary: PathBuf) -> Result<(Self, Arc<EnvironmentManager>)> {
        let home = root.join("harness/codex");
        ensure!(
            std::env::var_os("CODEX_HOME").map(PathBuf::from).as_ref() == Some(&home),
            "raw runner requires the isolated native history directory"
        );
        tokio::fs::create_dir_all(&home)
            .await
            .context("create native history directory")?;
        // The proof path can exceed Unix socket limits. This dedicated 0700 directory
        // stays under the original caller's private root, independently of child HOME.
        let socket_directory = tempfile::Builder::new()
            .prefix("raw-files-")
            .tempdir_in(configuration::caller_state_root()?)
            .context("create private raw socket directory")?;
        let socket_path = socket_directory.path().join("rpc.sock");
        ensure!(
            socket_path.as_os_str().len() < 104,
            "private raw socket path is too long"
        );
        let absolute_socket = AbsolutePathBuf::from_absolute_path(&socket_path)
            .context("invalid private raw socket path")?;
        let raw_overrides = configuration::overrides()
            .into_iter()
            .map(|(key, value)| format!("{key}={value}"))
            .collect();
        let transport = AppServerTransport::UnixSocket {
            socket_path: absolute_socket.clone(),
        };
        let (publish, published) = oneshot::channel();
        let mut runner = tokio::spawn(async move {
            run_main_with_transport_options_and_environment_manager(
                Arg0DispatchPaths {
                    codex_self_exe: Some(binary),
                    ..Default::default()
                },
                CliConfigOverrides { raw_overrides },
                LoaderOverrides {
                    ignore_user_config: true,
                    ignore_project_config: true,
                    ..Default::default()
                },
                false,
                false,
                transport,
                SessionSource::Exec,
                AppServerWebsocketAuthSettings::default(),
                AppServerRuntimeOptions {
                    plugin_startup_tasks: PluginStartupTasks::Skip,
                    remote_control_startup_mode: RemoteControlStartupMode::DisabledEphemeral,
                    install_shutdown_signal_handler: true,
                    ..Default::default()
                },
                publish,
            )
            .await
            .context("raw native runner failed")
        });
        let startup = async {
            // Publication is not readiness: also wait for the socket and complete
            // the maintained client's initialize/initialized exchange exactly once.
            let manager = published.await.context("raw manager was not published")?;
            while !tokio::fs::try_exists(&socket_path)
                .await
                .context("inspect raw socket")?
            {
                sleep(Duration::from_millis(20)).await;
            }
            let client = RemoteAppServerClient::connect(RemoteAppServerConnectArgs {
                endpoint: RemoteAppServerEndpoint::UnixSocket {
                    socket_path: absolute_socket,
                },
                client_name: "parsar_raw_files_probe".to_owned(),
                client_version: "1".to_owned(),
                experimental_api: true,
                mcp_server_openai_form_elicitation: false,
                opt_out_notification_methods: Vec::new(),
                channel_capacity: 1024,
            })
            .await
            .context("raw initialize exchange failed")?;
            Ok::<_, anyhow::Error>((client, manager))
        };
        let started = tokio::select! {
            result = &mut runner => {
                result.context("raw runner task failed during startup")??;
                bail!("raw runner stopped during startup");
            }
            result = timeout(Duration::from_secs(45), startup) => {
                result.context("raw startup timed out").and_then(|result| result)
            }
        };
        let (mut client, manager) = match started {
            Ok(started) => started,
            Err(error) => {
                // No initialized owner is returned on partial startup. The dedicated
                // process exits after this bounded failure; no request is replayed.
                runner.abort();
                let _ = runner.await;
                return Err(error);
            }
        };
        let sender = AppServerRequestHandle::Remote(client.request_handle());
        let observations = Observations::default();
        let state = observations.clone();
        let (stop, mut stopped) = oneshot::channel();
        let drain = tokio::spawn(async move {
            let result = async {
                loop {
                    tokio::select! {
                        _ = &mut stopped => return Ok::<_, anyhow::Error>(()),
                        event = client.next_event() => match event {
                            Some(AppServerEvent::ServerNotification(notification)) => state.record(*notification)?,
                            Some(AppServerEvent::ServerRequest(request)) => {
                                state.fail("unexpected native client request")?;
                                timeout(Duration::from_secs(5), client.reject_server_request(request.id().clone(), JSONRPCErrorError {
                                    code: -32601,
                                    message: "Unexpected client request in shared files probe".to_owned(),
                                    data: None,
                                })).await.context("native client request rejection timed out")?
                                    .context("native client request rejection failed")?;
                            }
                            Some(AppServerEvent::Lagged { .. }) => state.fail("native Lagged event observed")?,
                            Some(AppServerEvent::Disconnected { .. }) | None => {
                                state.fail("raw native event stream closed early")?;
                                return Ok(());
                            }
                        }
                    }
                }
            }.await;
            // The native client has an unbounded event queue. Observation limits
            // bound retained evidence only; this probe does not qualify backpressure.
            let stopped = client
                .shutdown()
                .await
                .context("raw native client shutdown failed");
            result.and(stopped)
        });
        Ok((
            Self {
                sender,
                observations,
                stop,
                drain,
                runner,
                socket_directory,
            },
            manager,
        ))
    }

    pub async fn request(&self, id: i64, method: &str, params: Value) -> Result<Value> {
        self.observations.healthy()?;
        ensure!(
            !self.runner.is_finished(),
            "raw native runner stopped before request"
        );
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
        let Self {
            sender,
            observations,
            stop,
            mut drain,
            mut runner,
            socket_directory,
        } = self;
        drop(sender);
        let _ = stop.send(());
        let client_stopped = match timeout(Duration::from_secs(15), &mut drain).await {
            Ok(result) => result
                .context("raw event drain task failed")
                .and_then(|result| result),
            Err(_) => {
                drain.abort();
                let _ = drain.await;
                Err(anyhow!("raw client shutdown exceeded fixture deadline"))
            }
        };
        let runner_stopped = if runner.is_finished() {
            let result = (&mut runner).await.context("raw runner task failed")?;
            result.and(Err(anyhow!("raw runner stopped before fixture shutdown")))
        } else {
            // Closing the socket client does not stop the multi-client raw listener.
            // This executable owns its whole process; SIGHUP requests native graceful
            // shutdown, and success requires joining that runner, not just the client.
            let signal = timeout(
                Duration::from_secs(5),
                tokio::process::Command::new("/bin/kill")
                    .args(["-HUP", &std::process::id().to_string()])
                    .kill_on_drop(true)
                    .status(),
            )
            .await
            .context("raw shutdown signal timed out")
            .and_then(|result| result.context("raw shutdown signal failed"))
            .and_then(|status| {
                ensure!(status.success(), "raw shutdown signal was rejected");
                Ok(())
            });
            match signal {
                Ok(()) => match timeout(Duration::from_secs(30), &mut runner).await {
                    Ok(result) => result
                        .context("raw runner task failed")
                        .and_then(|result| result),
                    Err(_) => {
                        runner.abort();
                        let _ = runner.await;
                        Err(anyhow!("raw runner shutdown exceeded fixture deadline"))
                    }
                },
                Err(error) => {
                    runner.abort();
                    let _ = runner.await;
                    Err(error)
                }
            }
        };
        drop(socket_directory);
        runner_stopped?;
        client_stopped?;
        observations.healthy()
    }
}

pub fn annotate(proof: &mut Value) {
    proof["status"] = json!("raw_native_files_characterized");
    proof["transport"] = json!("raw_unix_socket");
    proof["initialize_completed"] = json!(true);
    proof["limitations"] = json!([
        "The pinned native remote client has an unbounded consumer event queue. This finite workload does not qualify production memory or backpressure.",
        "Readiness-gated output checks do not establish complete native early-output capture; that limitation remains deferred.",
        "This private raw composition does not authorize production adoption, idle ownership or credential lifetime.",
        "Native cancellation is a required subsequent composition slice and is not exercised here.",
        "Native runner shutdown may abort internal tasks; joining it is not proof of executor OS quiescence.",
        "Direct native filesystem checks do not establish authorization, workspace confinement, public pagination or file_id semantics."
    ]);
}
