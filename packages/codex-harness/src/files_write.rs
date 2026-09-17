use super::{OperationError, process_output::Output};
use crate::options::WriteBinding;
use codex_exec_server::{
    Environment, ExecParams, ExecProcess, FileSystemSandboxContext, ProcessId, WriteStatus,
};
use codex_protocol::models::PermissionProfile;
use codex_protocol::permissions::{
    FileSystemAccessMode, FileSystemPath, FileSystemSandboxEntry, FileSystemSandboxPolicy,
    FileSystemSpecialPath, NetworkSandboxPolicy,
};
use codex_sandboxing::SandboxType;
use codex_utils_path_uri::PathUri;
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::path::PathBuf;
use uuid::Uuid;

pub(super) const MAX_BYTES: usize = 50 * 1024 * 1024;
const CHUNK_BYTES: usize = 64 * 1024;

pub(super) struct Upload {
    pub binding: WriteBinding,
    pub workspace: PathBuf,
    pub relative: String,
    pub size: usize,
    pub bytes: Vec<u8>,
}

pub(super) async fn install(
    environment: &Environment,
    upload: Upload,
) -> Result<Value, OperationError> {
    let invalid = |_| OperationError::Rejected("unsupported");
    let workspace = PathUri::from_host_native_path(&upload.workspace).map_err(invalid)?;
    let policy = FileSystemSandboxPolicy::restricted(vec![
        FileSystemSandboxEntry::new(
            FileSystemPath::Path {
                path: PathUri::from_host_native_path(&upload.binding.parent).map_err(invalid)?,
            },
            FileSystemAccessMode::Write,
        ),
        FileSystemSandboxEntry::new(
            FileSystemPath::Path {
                path: PathUri::from_host_native_path(&upload.binding.helper).map_err(invalid)?,
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
        workspace.clone(),
    );
    let started = environment
        .get_exec_backend()
        .start(ExecParams {
            process_id: ProcessId::from(format!("file-write-{}", Uuid::new_v4())),
            argv: vec![
                upload.binding.helper.to_string_lossy().into_owned(),
                upload.workspace.to_string_lossy().into_owned(),
                upload.relative,
                upload.size.to_string(),
                upload.binding.staging.to_string_lossy().into_owned(),
            ],
            cwd: workspace,
            shell_snapshot: None,
            env_policy: None,
            env: HashMap::new(),
            tty: false,
            pipe_stdin: true,
            arg0: None,
            sandbox: Some(sandbox),
            enforce_managed_network: false,
            managed_network: None,
            network_proxy: None,
        })
        .await
        .map_err(|_| OperationError::Unsettled)?;
    let process = started.process.as_ref();
    if !matches!(started.sandbox_type, Some(SandboxType::LinuxSeccomp)) {
        return stop_unknown(process).await;
    }
    transfer(process, &upload.bytes).await
}

async fn transfer(process: &dyn ExecProcess, bytes: &[u8]) -> Result<Value, OperationError> {
    let digest = Sha256::digest(bytes);
    for chunk in bytes
        .chunks(CHUNK_BYTES)
        .chain(std::iter::once(digest.as_slice()))
    {
        // Native write owns its request identity. Never recapture a connection or
        // retry a chunk here; Accepted only acknowledges queued stdin.
        match process.write(chunk.to_vec()).await {
            Ok(response) if matches!(response.status, WriteStatus::Accepted) => {}
            _ => return stop_unknown(process).await,
        }
    }
    let mut output = Output::default();
    loop {
        let response = process
            .read(Some(output.after()), None, Some(1000))
            .await
            .map_err(|_| OperationError::Unsettled)?;
        if response.failure.is_some() {
            return Err(OperationError::Unsettled);
        }
        if output.append(&response).is_err() {
            return stop_unknown(process).await;
        }
        if response.closed && response.exited {
            if response.exit_code != Some(0) || response.sandbox_denied {
                return Err(OperationError::Unsettled);
            }
            return decode(&output.bytes, bytes.len());
        }
    }
}

async fn stop_unknown(process: &dyn ExecProcess) -> Result<Value, OperationError> {
    // Best-effort termination cannot establish whether replacement committed.
    // The existing retained owner deadline bounds this attempt and any wait.
    let _ = process.terminate().await;
    Err(OperationError::Unsettled)
}

#[derive(Deserialize)]
#[serde(tag = "outcome", rename_all = "snake_case", deny_unknown_fields)]
enum Receipt {
    Completed { version: u32, size_bytes: usize },
    Failed { version: u32, error: String },
}

fn decode(bytes: &[u8], expected: usize) -> Result<Value, OperationError> {
    match serde_json::from_slice::<Receipt>(bytes) {
        Ok(Receipt::Completed {
            version: 1,
            size_bytes,
        }) if size_bytes == expected => {
            Ok(json!({"write":{"size_bytes":size_bytes,"committed":true}}))
        }
        Ok(Receipt::Failed { version: 1, error })
            if matches!(error.as_str(), "invalid_input" | "write_failed") =>
        {
            Err(OperationError::Rejected("native_error"))
        }
        _ => Err(OperationError::Unsettled),
    }
}

#[cfg(test)]
#[path = "files_write_process_tests.rs"]
mod tests;
