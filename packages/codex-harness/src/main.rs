mod files;
mod options;

use anyhow::{Context, Result};
use clap::Parser;
use codex_app_server::{
    AppServerRuntimeOptions, AppServerTransport, AppServerWebsocketAuthSettings,
    RemoteControlStartupMode, run_main_with_transport_options_and_environment_manager,
};
use codex_arg0::{Arg0DispatchPaths, Arg0PathEntryGuard, arg0_dispatch};
use codex_config::LoaderOverrides;
use codex_protocol::protocol::SessionSource;
use std::future::Future;
use std::time::Duration;
use tokio::sync::oneshot;

fn main() -> Result<()> {
    let (binding, _native_paths) = prepare_native();
    let cli = options::Cli::parse();
    run_owned_runtime(run_harness(cli, binding?))
}

fn prepare_native() -> (Result<options::Binding>, Option<Arg0PathEntryGuard>) {
    // Freeze operator selectors before native dotenv loading can change the
    // environment. Native helper dispatch and CLI help may exit without them.
    let binding = options::Binding::from_environment();
    let paths = arg0_dispatch();
    (binding, paths)
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

async fn run_harness(cli: options::Cli, binding: options::Binding) -> Result<()> {
    let overrides = cli.overrides()?;
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
    fn native_bootstrap_loads_credentials_and_freezes_binding() -> Result<()> {
        let state =
            std::path::PathBuf::from(std::env::var_os("HOME").context("HOME")?).join(".parsar");
        let home = tempfile::Builder::new().prefix("hb-").tempdir_in(state)?;
        std::fs::write(
            home.path().join(".env"),
            "PARSAR_HARNESS_TEST_PROVIDER_KEY=from-native-dotenv\nPARSAR_CODEX_HARNESS_WORKSPACE=/wrong\nCODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID=wrong\n",
        )?;
        let output = Command::new(std::env::current_exe()?)
            .args(["--exact", "tests::native_bootstrap_child", "--nocapture"])
            .env("PARSAR_HARNESS_BOOTSTRAP_TEST", "1")
            .env_remove("PARSAR_HARNESS_TEST_PROVIDER_KEY")
            .env("CODEX_HOME", home.path())
            .env("PARSAR_CODEX_HARNESS_NATIVE", "/operator/codex")
            .env("PARSAR_CODEX_HARNESS_WORKSPACE", "/operator/workspace")
            .env("PARSAR_CODEX_HARNESS_IPC_ROOT", home.path().join("ipc"))
            .env(
                "PARSAR_CODEX_HARNESS_ENVIRONMENT",
                "11111111-1111-4111-8111-111111111111",
            )
            .env(
                "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID",
                "11111111-1111-4111-8111-111111111111",
            )
            .output()?;
        assert!(output.status.success(), "{output:?}");
        assert!(String::from_utf8_lossy(&output.stdout).contains("native bootstrap verified"));
        Ok(())
    }

    #[test]
    fn native_bootstrap_child() -> Result<()> {
        if std::env::var_os("PARSAR_HARNESS_BOOTSTRAP_TEST").is_none() {
            return Ok(());
        }
        let (binding, _native_paths) = prepare_native();
        let binding = binding?;
        assert_eq!(
            std::env::var("PARSAR_HARNESS_TEST_PROVIDER_KEY")?,
            "from-native-dotenv"
        );
        assert_eq!(std::env::var("PARSAR_CODEX_HARNESS_WORKSPACE")?, "/wrong");
        assert_eq!(
            binding.workspace,
            std::path::Path::new("/operator/workspace")
        );
        assert_eq!(
            std::env::var("CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID")?,
            binding.environment
        );
        println!("native bootstrap verified");
        Ok(())
    }

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
