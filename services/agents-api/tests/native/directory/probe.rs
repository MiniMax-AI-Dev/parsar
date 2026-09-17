use std::collections::HashMap;
use std::path::PathBuf;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use codex_exec_server::{
    EnvironmentManager, ExecOutputStream, ExecParams, ExecProcess, FileSystemSandboxContext,
    ProcessId,
};
use codex_http_client::{HttpClientFactory, OutboundProxyPolicy};
use codex_protocol::models::PermissionProfile;
use codex_protocol::permissions::{
    FileSystemAccessMode, FileSystemPath, FileSystemSandboxEntry, FileSystemSandboxPolicy,
    FileSystemSpecialPath, NetworkSandboxPolicy,
};
use codex_utils_path_uri::PathUri;
use tokio::time::timeout;

const TIMEOUT: Duration = Duration::from_secs(15);

#[tokio::main(flavor = "multi_thread", worker_threads = 4)]
async fn main() -> Result<()> {
    timeout(Duration::from_secs(90), run()).await??;
    Ok(())
}

async fn run() -> Result<()> {
    let proof = PathBuf::from(std::env::var("PARSAR_NATIVE_ENV_PROOF")?);
    let workspace = PathBuf::from(std::env::var("PARSAR_DIRECTORY_WORKSPACE")?);
    let helper = PathBuf::from("/usr/local/bin/scoped-directory");
    let manager = EnvironmentManager::from_env(
        None,
        HttpClientFactory::new(OutboundProxyPolicy::ReqwestDefault),
    )
    .await?;
    ensure!(manager.try_local_environment().is_none(), "local fallback");
    let environment = manager
        .default_environment()
        .context("remote environment")?;
    ensure!(environment.is_remote(), "remote backend required");
    timeout(TIMEOUT, environment.wait_until_ready()).await??;
    let cwd = PathUri::from_host_native_path(&workspace)?;
    let policy = FileSystemSandboxPolicy::restricted(vec![
        FileSystemSandboxEntry::new(
            FileSystemPath::Path {
                path: PathUri::from_host_native_path(&workspace)?,
            },
            FileSystemAccessMode::Read,
        ),
        FileSystemSandboxEntry::new(
            FileSystemPath::Path {
                path: PathUri::from_host_native_path(&helper)?,
            },
            FileSystemAccessMode::Read,
        ),
        FileSystemSandboxEntry::new(
            FileSystemPath::Special {
                value: FileSystemSpecialPath::Minimal,
            },
            FileSystemAccessMode::Read,
        ),
    ]);
    let sandbox = FileSystemSandboxContext::from_permission_profile_with_cwd(
        PermissionProfile::from_runtime_permissions(&policy, NetworkSandboxPolicy::Restricted),
        cwd.clone(),
    );
    let mut observed = Vec::new();
    for (index, (relative, limit)) in [
        ("", 64),
        ("sub", 1),
        ("empty", 1),
        ("a/sub", 16),
        ("../outside", 2),
        ("sub", 0),
    ]
    .into_iter()
    .enumerate()
    {
        let mut request = params(
            &format!("directory-{index}"),
            &cwd,
            vec![
                helper.to_string_lossy().into_owned(),
                workspace.to_string_lossy().into_owned(),
                relative.into(),
                limit.to_string(),
            ],
        );
        request.sandbox = Some(sandbox.clone());
        let started = environment.get_exec_backend().start(request).await?;
        let sandbox_type = format!("{:?}", started.sandbox_type);
        ensure!(
            sandbox_type == "Some(LinuxSeccomp)",
            "required Linux sandbox missing: {sandbox_type}"
        );
        let (stdout, stderr, exit) = read_closed(started.process.as_ref()).await?;
        ensure!(
            exit == Some(0) && stderr.is_empty(),
            "directory helper execution failed: {:?} {}",
            exit,
            String::from_utf8_lossy(&stderr)
        );
        let value: serde_json::Value = serde_json::from_slice(&stdout)?;
        ensure!(value["version"] == 1, "helper version mismatch");
        if relative == "" {
            let entries = value["directory"]["entries"]
                .as_array()
                .context("root entries")?;
            ensure!(entries.len() == 4, "root count mismatch: {value}");
            ensure!(
                entries
                    .iter()
                    .any(|e| e["name"] == "retained.txt" && e["size_bytes"] == 8),
                "retained metadata mismatch"
            );
        } else if relative == "sub" && limit == 1 {
            ensure!(
                value["directory"]["entries"]
                    .as_array()
                    .context("limited entries")?
                    .len()
                    == 1
                    && value["directory"]["truncated"] == true,
                "bounded scan mismatch"
            );
        } else if relative == "empty" {
            ensure!(
                value["directory"]["entries"] == serde_json::json!([])
                    && value["directory"]["truncated"] == false,
                "empty mismatch"
            );
        } else {
            ensure!(value["error"].is_string(), "expected rejection: {value}");
        }
        observed.push(serde_json::json!({"path":relative,"limit":limit,"response":value,"sandbox":sandbox_type,"exit":exit,"closed":true}));
    }
    std::fs::write(
        proof.join("directory-native.json"),
        serde_json::to_vec_pretty(
            &serde_json::json!({"native_source":"3d2ee51ca2d5db578f328aa75e20aa22c0197c9a","observations":observed,"model_calls":0,"scope":"authenticated native process RPC, actual Docker executor, explicit argv, read-only sandbox, helper output/exit/close; not public API acceptance"}),
        )?,
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

async fn read_closed(process: &dyn ExecProcess) -> Result<(Vec<u8>, Vec<u8>, Option<i32>)> {
    timeout(TIMEOUT, async {
        let mut after = None;
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        loop {
            let result = process.read(after, Some(65536), Some(1000)).await?;
            ensure!(result.failure.is_none(), "process failed");
            for chunk in result.chunks {
                match chunk.stream {
                    ExecOutputStream::Stdout => stdout.extend(chunk.chunk.0),
                    ExecOutputStream::Stderr => stderr.extend(chunk.chunk.0),
                    ExecOutputStream::Pty => anyhow::bail!("unexpected PTY"),
                }
            }
            ensure!(
                stdout.len() + stderr.len() <= 2 * 1024 * 1024,
                "output cap exceeded"
            );
            if result.closed {
                return Ok((stdout, stderr, result.exit_code));
            }
            after = result.next_seq.checked_sub(1);
        }
    })
    .await?
}
