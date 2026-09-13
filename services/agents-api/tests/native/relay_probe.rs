use std::collections::HashMap;
use std::path::PathBuf;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use codex_exec_server::{
    EnvironmentConnectionState, EnvironmentManager, EnvironmentObservedStatus, ExecOutputStream,
    ExecParams, ExecProcess, ProcessId, ReadFileOptions, WriteFileOptions,
};
use codex_http_client::{HttpClientFactory, OutboundProxyPolicy};
use codex_utils_path_uri::PathUri;
use tokio::time::timeout;

const TIMEOUT: Duration = Duration::from_secs(40);

#[tokio::main(flavor = "multi_thread", worker_threads = 4)]
async fn main() -> Result<()> {
    timeout(Duration::from_secs(100), run()).await??;
    Ok(())
}

async fn run() -> Result<()> {
    let root = PathBuf::from(std::env::var("PARSAR_NATIVE_ENV_PROOF")?);
    let phase = std::env::args().nth(1).context("phase required")?;
    let manager = EnvironmentManager::from_env(
        None,
        HttpClientFactory::new(OutboundProxyPolicy::ReqwestDefault),
    )
    .await?;
    ensure!(
        manager.try_local_environment().is_none(),
        "local fallback configured"
    );
    let environment = manager
        .default_environment()
        .context("remote environment")?;
    ensure!(environment.is_remote(), "expected remote backend");
    timeout(TIMEOUT, environment.wait_until_ready()).await??;
    let cwd = PathUri::from_host_native_path(root.join("workspace"))?;
    let file = PathUri::from_host_native_path(root.join("workspace/large.bin"))?;
    let marker_path = root.join("workspace/start-marker.txt");
    let marker = PathUri::from_host_native_path(&marker_path)?;
    let contents: Vec<u8> = (0..128 * 1024).map(|index| (index % 251) as u8).collect();
    let fs = environment.get_filesystem();
    if phase == "fresh" {
        ensure!(
            fs.read_file(&file, ReadFileOptions::default(), None)
                .await?
                == contents,
            "retained file mismatch"
        );
        let start = fs
            .read_file(&marker, ReadFileOptions::default(), None)
            .await?;
        ensure!(
            String::from_utf8(start)?.lines().count() == 1,
            "command repeated"
        );
        std::fs::write(
            root.join("fresh.json"),
            r#"{"fresh_connection":true,"retained_file":true,"single_start":true}"#,
        )?;
        return Ok(());
    }
    ensure!(phase == "first", "unknown phase");
    let exec = environment.get_exec_backend();
    let mut controlled_params = params("retained", &cwd, vec![
        "/bin/sh".into(), "-c".into(),
        "printf '%s %s\\n' \"$1\" \"$$\" >> \"$2\"; printf 'READY\\n'; IFS= read -r first; printf 'FIRST:%s\\n' \"$first\"; IFS= read -r second; printf 'SECOND:%s\\n' \"$second\"; exit 9".into(),
        "relay".into(), "single-native-start".into(), marker_path.to_str().context("marker path")?.into(),
    ]);
    controlled_params.pipe_stdin = true;
    let controlled = exec.start(controlled_params).await?;
    read_until(controlled.process.as_ref(), b"READY\n").await?;
    let command = async {
        let started = exec
            .start(params(
                "concurrent",
                &cwd,
                vec![
                    "/bin/sh".into(),
                    "-c".into(),
                    "printf native-stdout; printf native-stderr >&2; exit 7".into(),
                ],
            ))
            .await?;
        let (stdout, stderr, exit) = read_closed(started.process.as_ref()).await?;
        ensure!(
            stdout == b"native-stdout" && stderr == b"native-stderr" && exit == Some(7),
            "command output mismatch"
        );
        Ok::<_, anyhow::Error>(())
    };
    let files = async {
        fs.write_file(&file, contents.clone(), WriteFileOptions::default(), None)
            .await?;
        ensure!(
            fs.read_file(&file, ReadFileOptions::default(), None)
                .await?
                == contents,
            "large file mismatch"
        );
        Ok::<_, anyhow::Error>(())
    };
    tokio::try_join!(command, files)?;
    let sleeper = exec
        .start(params(
            "terminate",
            &cwd,
            vec!["/bin/sleep".into(), "60".into()],
        ))
        .await?;
    sleeper.process.terminate().await?;
    let (_, _, terminated_exit) = read_closed(sleeper.process.as_ref()).await?;
    ensure!(terminated_exit.is_some(), "termination did not settle");
    environment.refresh_connection().await?;
    controlled.process.write(b"before-loss\n".to_vec()).await?;
    read_until(controlled.process.as_ref(), b"FIRST:before-loss\n").await?;
    let mut states = environment
        .subscribe_connection_state()
        .context("state receiver")?;
    ensure!(
        *states.borrow_and_update() == EnvironmentConnectionState::Connected,
        "not connected before injection"
    );
    std::fs::write(root.join("ready-to-disconnect"), b"ready")?;
    timeout(TIMEOUT, async {
        let mut disconnected = false;
        loop {
            states.changed().await?;
            match *states.borrow_and_update() {
                EnvironmentConnectionState::Disconnected => disconnected = true,
                EnvironmentConnectionState::Connected if disconnected => {
                    return Ok::<_, anyhow::Error>(());
                }
                EnvironmentConnectionState::Connected => {}
            }
        }
    })
    .await??;
    ensure!(
        environment.status().await == EnvironmentObservedStatus::Ready,
        "recovered status is not ready"
    );
    controlled.process.write(b"after-loss\n".to_vec()).await?;
    let (stdout, stderr, exit) = read_closed(controlled.process.as_ref()).await?;
    ensure!(
        stdout == b"READY\nFIRST:before-loss\nSECOND:after-loss\n"
            && stderr.is_empty()
            && exit == Some(9),
        "retained process outcome mismatch"
    );
    let start = String::from_utf8(
        fs.read_file(&marker, ReadFileOptions::default(), None)
            .await?,
    )?;
    ensure!(
        start.lines().count() == 1 && start.starts_with("single-native-start "),
        "command start repeated"
    );
    std::fs::write(
        root.join("first.json"),
        serde_json::to_vec_pretty(&serde_json::json!({
            "native_remote":true,"large_file_bytes":contents.len(),"concurrent_command":true,
            "stdout_stderr":true,"command_exit":7,"termination_exit":terminated_exit,
            "same_key_refresh":true,"disconnected_connected":true,"same_process_recovered":true,
            "controlled_exit":9,"single_command_start":true,"model_calls":0
        }))?,
    )?;
    Ok(())
}

fn params(id: &str, cwd: &PathUri, argv: Vec<String>) -> ExecParams {
    ExecParams {
        process_id: ProcessId::from(id),
        argv,
        cwd: cwd.clone(),
        shell_snapshot: None,
        env_policy: None,
        env: HashMap::new(),
        tty: false,
        pipe_stdin: false,
        arg0: None,
        sandbox: None,
        enforce_managed_network: false,
        managed_network: None,
        network_proxy: None,
    }
}

async fn read_until(process: &dyn ExecProcess, needle: &[u8]) -> Result<()> {
    timeout(TIMEOUT, async {
        let mut after = None;
        let mut output = Vec::new();
        loop {
            let result = process.read(after, None, Some(1000)).await?;
            ensure!(result.failure.is_none(), "process failed");
            for chunk in result.chunks {
                output.extend(chunk.chunk.0);
            }
            if output.windows(needle.len()).any(|part| part == needle) {
                return Ok(());
            }
            ensure!(!result.closed, "process closed before checkpoint");
            after = result.next_seq.checked_sub(1);
        }
    })
    .await?
}

async fn read_closed(process: &dyn ExecProcess) -> Result<(Vec<u8>, Vec<u8>, Option<i32>)> {
    timeout(TIMEOUT, async {
        let mut after = None;
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        loop {
            let result = process.read(after, None, Some(1000)).await?;
            ensure!(result.failure.is_none(), "process failed");
            for chunk in result.chunks {
                match chunk.stream {
                    ExecOutputStream::Stdout => stdout.extend(chunk.chunk.0),
                    ExecOutputStream::Stderr => stderr.extend(chunk.chunk.0),
                    ExecOutputStream::Pty => anyhow::bail!("unexpected PTY"),
                }
            }
            if result.closed {
                return Ok((stdout, stderr, result.exit_code));
            }
            after = result.next_seq.checked_sub(1);
        }
    })
    .await?
}
