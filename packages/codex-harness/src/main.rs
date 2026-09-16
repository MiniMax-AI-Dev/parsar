mod files;
mod options;

use anyhow::{Context, Result};
use clap::Parser;
use codex_app_server::{
    AppServerRuntimeOptions, AppServerTransport, AppServerWebsocketAuthSettings,
    RemoteControlStartupMode, run_main_with_transport_options_and_environment_manager,
};
use codex_arg0::Arg0DispatchPaths;
use codex_config::LoaderOverrides;
use codex_protocol::protocol::SessionSource;
use tokio::sync::oneshot;

#[tokio::main]
async fn main() -> Result<()> {
    let cli = options::Cli::parse();
    let overrides = cli.overrides()?;
    let binding = options::Binding::from_environment()?;
    binding.check_native().await?;
    let socket = files::PrivateSocket::bind(&binding.ipc_root)?;
    let (publish, published) = oneshot::channel();
    let runner = run_main_with_transport_options_and_environment_manager(
        Arg0DispatchPaths {
            codex_self_exe: Some(binding.native_binary.clone()),
            ..Default::default()
        },
        overrides,
        LoaderOverrides::default(),
        false,
        false,
        AppServerTransport::Stdio,
        SessionSource::VSCode,
        AppServerWebsocketAuthSettings::default(),
        AppServerRuntimeOptions {
            remote_control_startup_mode: RemoteControlStartupMode::DisabledEphemeral,
            ..Default::default()
        },
        publish,
    );
    // The stock single-client runner owns stdin/stdout. No typed event client or
    // forwarding queue is inserted between it and the existing Go RPC caller.
    tokio::select! {
        biased;
        result = runner => result.context("native harness stopped"),
        result = socket.serve(published, &binding) => result.context("private metadata endpoint stopped"),
    }
    // Dropping the other future stops local admission and releases its manager.
    // Process exit is not evidence that remote mutations or descendants retired.
}
