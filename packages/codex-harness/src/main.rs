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
use std::future::Future;
use std::time::Duration;
use tokio::sync::oneshot;

fn main() -> Result<()> {
    run_owned_runtime(run_harness())
}

fn run_owned_runtime(operation: impl Future<Output = Result<()>>) -> Result<()> {
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?;
    let result = runtime.block_on(operation);
    // Native stdin uses an uncancellable blocking read. Bound local teardown so
    // the caller observes process exit even while it keeps stdin open.
    runtime.shutdown_timeout(Duration::from_secs(1));
    result
}

async fn run_harness() -> Result<()> {
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

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Read;
    use std::process::{Command, Stdio};
    use std::time::Instant;

    #[test]
    fn runtime_failure_exits_with_stdin_open() {
        let mut child = Command::new(std::env::current_exe().expect("test executable"))
            .args(["--exact", "tests::runtime_failure_child", "--nocapture"])
            .env("PARSAR_HARNESS_SHUTDOWN_TEST", "1")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .expect("start shutdown child");
        let held_stdin = child.stdin.take().expect("child stdin");
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            if child.try_wait().expect("poll child").is_some() {
                break;
            }
            if Instant::now() >= deadline {
                let _ = child.kill();
                let _ = child.wait();
                panic!("runtime shutdown waited for open stdin");
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        let output = child.wait_with_output().expect("read child EOF");
        drop(held_stdin);
        assert!(output.status.success(), "{output:?}");
        assert!(String::from_utf8_lossy(&output.stdout).contains("runtime failure returned"));
    }

    #[test]
    fn runtime_failure_child() {
        if std::env::var_os("PARSAR_HARNESS_SHUTDOWN_TEST").is_none() {
            return;
        }
        let error = run_owned_runtime(async {
            let (started, wait_started) = oneshot::channel();
            // Exercise the blocking-pool read used by native Tokio stdin, with
            // a deterministic admission signal instead of a timing assumption.
            tokio::task::spawn_blocking(move || {
                started.send(()).expect("signal blocking read");
                let _ = std::io::stdin().read(&mut [0_u8; 1]);
            });
            wait_started.await?;
            anyhow::bail!("controlled native operation failure")
        })
        .expect_err("native failure survives runtime shutdown");
        assert_eq!(error.to_string(), "controlled native operation failure");
        println!("runtime failure returned");
    }
}
