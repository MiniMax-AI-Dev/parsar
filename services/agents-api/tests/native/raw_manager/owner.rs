use anyhow::{Context, Result, ensure};
use codex_app_server::{
    AppServerRuntimeOptions, AppServerTransport, AppServerWebsocketAuthSettings,
    PluginStartupTasks, RemoteControlStartupMode,
    run_main_with_transport_options_and_environment_manager,
};
use codex_arg0::Arg0DispatchPaths;
use codex_config::LoaderOverrides;
use codex_exec_server::EnvironmentManager;
use codex_protocol::protocol::SessionSource;
use codex_utils_cli::CliConfigOverrides;
use codex_utils_path_uri::PathUri;
use std::io::ErrorKind;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::oneshot;

pub const ENVIRONMENT_ID: &str = "raw-manager-smoke";
pub const CONTENT: &[u8] = b"typed Files through the stock raw runner\n";

async fn run(
    native: &Path,
    transport: AppServerTransport,
    invalid: bool,
    sender: oneshot::Sender<Arc<EnvironmentManager>>,
) -> std::io::Result<()> {
    let mut loader = LoaderOverrides::without_managed_config_for_tests();
    loader.ignore_user_config = true;
    loader.ignore_project_config = true;
    run_main_with_transport_options_and_environment_manager(
        Arg0DispatchPaths {
            codex_self_exe: Some(native.to_path_buf()),
            ..Default::default()
        },
        CliConfigOverrides {
            raw_overrides: if invalid {
                vec!["missing-equals".into()]
            } else {
                vec![]
            },
        },
        loader,
        true,
        false,
        transport,
        SessionSource::Exec,
        AppServerWebsocketAuthSettings::default(),
        AppServerRuntimeOptions {
            plugin_startup_tasks: PluginStartupTasks::Skip,
            remote_control_startup_mode: RemoteControlStartupMode::DisabledEphemeral,
            install_shutdown_signal_handler: false,
            ..Default::default()
        },
        sender,
    )
    .await
}

pub async fn serve(native: &Path, root: &Path) -> Result<()> {
    let (sender, receiver) = oneshot::channel::<Arc<EnvironmentManager>>();
    let files = async {
        let manager = receiver.await.context("manager was not published")?;
        ensure!(
            manager.try_local_environment().is_none(),
            "local fallback is configured"
        );
        // This ID can appear only after the controller's initialized raw RPC.
        let environment = tokio::time::timeout(Duration::from_secs(20), async {
            loop {
                if let Some(environment) = manager.get_environment(ENVIRONMENT_ID) {
                    break environment;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .context("raw environment/add did not reach the published manager")?;
        ensure!(
            environment.is_remote(),
            "raw-added environment is not remote"
        );
        let filesystem = environment.get_filesystem();
        let path = PathUri::from_host_native_path(root.join("remote-file.txt"))?;
        filesystem
            .write_file(&path, CONTENT.to_vec(), Default::default(), None)
            .await?;
        let bytes = filesystem
            .read_file(&path, Default::default(), None)
            .await?;
        ensure!(
            bytes == CONTENT,
            "typed remote read returned different bytes"
        );
        tokio::fs::write(
            root.join("typed-files.pending"),
            b"{\"shared_manager\":true,\"typed_files\":true}\n",
        )
        .await?;
        tokio::fs::rename(
            root.join("typed-files.pending"),
            root.join("typed-files.json"),
        )
        .await?;
        Ok::<_, anyhow::Error>(())
    };
    tokio::try_join!(
        async {
            run(native, AppServerTransport::Stdio, false, sender)
                .await
                .map_err(Into::into)
        },
        files
    )?;
    Ok(())
}

pub async fn failure(native: &Path, mode: &str) -> Result<()> {
    let (sender, mut receiver) = oneshot::channel::<Arc<EnvironmentManager>>();
    if mode == "receiver-dropped" {
        drop(receiver);
        let error = run(native, AppServerTransport::Stdio, false, sender)
            .await
            .err()
            .context("dropped receiver must fail startup")?;
        ensure!(
            error.kind() == ErrorKind::BrokenPipe,
            "wrong dropped-receiver error: {error}"
        );
    } else if mode == "startup-failure" {
        ensure!(
            run(native, AppServerTransport::Stdio, true, sender)
                .await
                .is_err()
        );
        ensure!(
            matches!(
                receiver.try_recv(),
                Err(oneshot::error::TryRecvError::Closed)
            ),
            "failed configuration published a manager"
        );
    } else {
        // A bind failure occurs after handle publication, proving it is not readiness.
        let occupied = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
        let transport = format!("ws://{}", occupied.local_addr()?).parse()?;
        ensure!(run(native, transport, false, sender).await.is_err());
        let manager = receiver
            .try_recv()
            .context("expected publication before bind failure")?;
        let weak = Arc::downgrade(&manager);
        drop(manager);
        ensure!(
            weak.upgrade().is_none(),
            "failed startup retained the manager"
        );
    }
    Ok(())
}
