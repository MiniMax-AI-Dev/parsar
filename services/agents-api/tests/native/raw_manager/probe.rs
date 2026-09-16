//! Opt-in no-model qualification of the additive raw-runner manager hook.

#[path = "raw_manager/owner.rs"]
mod owner;

use anyhow::{Context, Result, bail, ensure};
use codex_exec_server::ExecServerRuntimePaths;
use codex_http_client::{HttpClientFactory, OutboundProxyPolicy};
use serde_json::{Value, json};
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader, Lines};
use tokio::process::{Child, ChildStdout, Command};

const DEADLINE: Duration = Duration::from_secs(30);
// Match pinned arg0/async-utils stack sizing for large native futures.
const NATIVE_STACK_BYTES: usize = 16 * 1024 * 1024;
const CALLER_HOME_ENV: &str = "PARSAR_RAW_MANAGER_CALLER_HOME";

fn main() -> Result<()> {
    std::thread::Builder::new()
        .name("raw-manager-main".into())
        .stack_size(NATIVE_STACK_BYTES)
        .spawn(|| {
            tokio::runtime::Builder::new_multi_thread()
                .enable_all()
                .worker_threads(4)
                .thread_stack_size(NATIVE_STACK_BYTES)
                .build()?
                .block_on(run())
        })?
        .join()
        .map_err(|_| anyhow::anyhow!("native main thread panicked"))?
}

async fn run() -> Result<()> {
    let args: Vec<String> = std::env::args().collect();
    ensure!(
        args.len() == 4,
        "usage: probe MODE NATIVE_BINARY PROOF_DIRECTORY"
    );
    let native = Path::new(&args[2]);
    let root = Path::new(&args[3]);
    ensure!(
        native.is_absolute() && root.is_absolute(),
        "absolute paths are required"
    );
    let home_variable = match args[1].as_str() {
        "owner"
        | "child-receiver-dropped"
        | "child-startup-failure"
        | "child-late-startup-failure" => CALLER_HOME_ENV,
        _ => "HOME",
    };
    let caller_home =
        PathBuf::from(std::env::var_os(home_variable).context("caller HOME is missing")?);
    let root = proof_root(root, &caller_home)?;
    match args[1].as_str() {
        "owner" => owner::serve(native, &root).await,
        "child-receiver-dropped" | "child-startup-failure" | "child-late-startup-failure" => {
            owner::failure(native, args[1].trim_start_matches("child-")).await
        }
        "smoke" | "receiver-dropped" | "startup-failure" | "late-startup-failure" => {
            let root = tempfile::Builder::new()
                .prefix("raw-manager-")
                .tempdir_in(&root)?
                .keep();
            let result = tokio::time::timeout(DEADLINE, qualify(&args[1], native, &root)).await;
            println!(
                "{}",
                json!({"case":args[1],"evidence":root,"passed":matches!(&result, Ok(Ok(())))})
            );
            result.context("qualification timed out")?
        }
        _ => bail!("unknown qualification mode"),
    }
}

fn proof_root(root: &Path, caller_home: &Path) -> Result<PathBuf> {
    ensure!(caller_home.is_absolute(), "caller HOME must be absolute");
    let expected = caller_home
        .join(".parsar")
        .canonicalize()
        .context("resolve caller state directory")?;
    let actual = root.canonicalize().context("resolve proof directory")?;
    ensure!(
        actual.starts_with(expected),
        "proof directory must resolve under caller ~/.parsar"
    );
    Ok(actual)
}

fn child(mode: &str, native: &Path, root: &Path) -> Result<Child> {
    let home = root.join("codex");
    std::fs::create_dir_all(&home)?;
    let stderr = std::fs::File::create(root.join("owner.stderr"))?;
    Command::new(std::env::current_exe()?)
        .args([
            mode,
            native.to_str().context("native path is not UTF-8")?,
            root.to_str().context("proof path is not UTF-8")?,
        ])
        .env_clear()
        .env("HOME", root)
        // Retained fixture context, not authorization; native HOME stays isolated.
        .env(
            CALLER_HOME_ENV,
            std::env::var_os("HOME").context("caller HOME is missing")?,
        )
        .env("CODEX_HOME", home)
        .env("TMPDIR", root)
        .env("PATH", "/usr/bin:/bin")
        .env("CODEX_EXEC_SERVER_URL", "none")
        .env("RUST_LOG", "warn")
        .current_dir(root)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(stderr)
        .kill_on_drop(true)
        .spawn()
        .context("launch raw-runner owner")
}

async fn response(lines: &mut Lines<BufReader<ChildStdout>>, id: i64) -> Result<Value> {
    while let Some(line) = lines.next_line().await? {
        let value: Value = serde_json::from_str(&line).context("native stdout was not JSON")?;
        if value["id"] == id {
            ensure!(
                value.get("error").is_none(),
                "native request failed: {value}"
            );
            return Ok(value["result"].clone());
        }
        ensure!(
            value.get("id").is_none(),
            "unexpected native request/response: {value}"
        );
    }
    bail!("owner exited before response {id}")
}

async fn qualify(mode: &str, native: &Path, root: &Path) -> Result<()> {
    if mode != "smoke" {
        let mut child = child(&format!("child-{mode}"), native, root)?;
        ensure!(
            child.wait().await?.success(),
            "failure scenario did not meet its assertions"
        );
        return Ok(());
    }

    // Use the native exec-server, with the unchanged pinned executable for helpers.
    let reservation = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
    let endpoint = format!("ws://{}", reservation.local_addr()?);
    drop(reservation);
    let server_endpoint = endpoint.clone();
    let paths = ExecServerRuntimePaths::new(PathBuf::from(native), None)?;
    let executor = tokio::spawn(async move {
        codex_exec_server::run_main(
            &server_endpoint,
            paths,
            HttpClientFactory::new(OutboundProxyPolicy::ReqwestDefault),
        )
        .await
    });
    let executor = tokio_util::task::AbortOnDropHandle::new(executor);

    let mut owner = child("owner", native, root)?;
    let mut input = owner.stdin.take().context("missing owner stdin")?;
    let mut lines = BufReader::new(owner.stdout.take().context("missing owner stdout")?).lines();
    input.write_all(format!("{}\n", json!({"id":1,"method":"initialize","params":{"clientInfo":{"name":"parsar_raw_manager_probe","version":"1"},"capabilities":{"experimentalApi":true}}})).as_bytes()).await?;
    let initialized = response(&mut lines, 1).await?;
    ensure!(initialized.is_object(), "missing stock initialize response");
    input.write_all(b"{\"method\":\"initialized\"}\n").await?;
    input.write_all(format!("{}\n", json!({"id":2,"method":"environment/add","params":{"environmentId":owner::ENVIRONMENT_ID,"execServerUrl":endpoint}})).as_bytes()).await?;
    response(&mut lines, 2).await?;
    loop {
        if tokio::fs::try_exists(root.join("typed-files.json")).await? {
            break;
        }
        ensure!(
            owner.try_wait()?.is_none(),
            "owner exited before typed Files completed"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    let evidence: Value =
        serde_json::from_slice(&tokio::fs::read(root.join("typed-files.json")).await?)?;
    ensure!(evidence["shared_manager"] == true && evidence["typed_files"] == true);
    ensure!(tokio::fs::read(root.join("remote-file.txt")).await? == owner::CONTENT);
    input.write_all(format!("{}\n", json!({"id":3,"method":"environment/status","params":{"environmentId":owner::ENVIRONMENT_ID}})).as_bytes()).await?;
    ensure!(
        response(&mut lines, 3).await?["status"] == "ready",
        "raw manager lost its environment"
    );
    drop(input);
    while lines.next_line().await?.is_some() {}
    ensure!(
        owner.wait().await?.success(),
        "raw owner failed during EOF shutdown"
    );
    drop(executor);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn proof_root_rejects_component_traversal_and_symlink_escapes() -> Result<()> {
        let caller_home = PathBuf::from(std::env::var_os("HOME").context("HOME is missing")?);
        let task_root =
            PathBuf::from(std::env::var_os("TMPDIR").context("task TMPDIR is missing")?);
        let task_root = proof_root(&task_root, &caller_home)?;
        let fixture = tempfile::Builder::new()
            .prefix("raw-manager-path-test-")
            .tempdir_in(task_root)?;
        let home = fixture.path().join("caller");
        let valid = home.join(".parsar/proof");
        let outside = fixture.path().join("outside/.parsar");
        std::fs::create_dir_all(&valid)?;
        std::fs::create_dir_all(&outside)?;
        ensure!(proof_root(&valid, &home)? == valid.canonicalize()?);
        ensure!(
            proof_root(&outside, &home).is_err(),
            "a .parsar component is insufficient"
        );
        let traversal = home.join(".parsar/../../outside/.parsar");
        ensure!(
            proof_root(&traversal, &home).is_err(),
            "parent traversal escaped the state root"
        );
        #[cfg(unix)]
        {
            let link = home.join(".parsar/link");
            std::os::unix::fs::symlink(&outside, &link)?;
            ensure!(
                proof_root(&link, &home).is_err(),
                "symlink escaped the state root"
            );
        }
        Ok(())
    }
}
