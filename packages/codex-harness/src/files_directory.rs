use super::OperationError;
use codex_exec_server::{
    Environment, ExecParams, ExecProcess, FileSystemSandboxContext, ProcessId,
};
use codex_protocol::models::PermissionProfile;
use codex_protocol::permissions::{
    FileSystemAccessMode, FileSystemPath, FileSystemSandboxEntry, FileSystemSandboxPolicy,
    FileSystemSpecialPath, NetworkSandboxPolicy,
};
use codex_sandboxing::SandboxType;
use codex_utils_path_uri::PathUri;
use serde_json::Value;
use std::collections::HashMap;
use std::path::{Component, Path};
use uuid::Uuid;

#[path = "files_directory_output.rs"]
mod output;

pub(super) const MAX_ENTRIES: usize = 4096;

pub(super) async fn list(
    environment: &Environment,
    workspace: &PathUri,
    path: &PathUri,
    limit: usize,
    helper: Option<&Path>,
) -> Result<Value, OperationError> {
    let invalid = || OperationError::Rejected("unsupported");
    let root = workspace.to_abs_path().map_err(|_| invalid())?;
    let target = path.to_abs_path().map_err(|_| invalid())?;
    let helper = helper
        .filter(|helper| qualified_path(helper, root.as_path()))
        .ok_or_else(invalid)?;
    let relative = target
        .as_path()
        .strip_prefix(root.as_path())
        .ok()
        .and_then(Path::to_str)
        .ok_or_else(invalid)?;
    let policy = FileSystemSandboxPolicy::restricted(vec![
        FileSystemSandboxEntry::new(
            FileSystemPath::Path {
                path: workspace.clone(),
            },
            FileSystemAccessMode::Read,
        ),
        FileSystemSandboxEntry::new(
            FileSystemPath::Path {
                path: PathUri::from_host_native_path(helper).map_err(|_| invalid())?,
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
            process_id: ProcessId::from(format!("directory-{}", Uuid::new_v4())),
            argv: vec![
                helper.to_string_lossy().into_owned(),
                root.to_string_lossy().into_owned(),
                relative.into(),
                limit.to_string(),
            ],
            cwd: workspace.clone(),
            shell_snapshot: None,
            env_policy: None,
            env: HashMap::new(),
            tty: false,
            pipe_stdin: false,
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
        return stop_and_reject(process).await;
    }
    let mut output = output::Output::default();
    loop {
        // Uncapped snapshots expose the native global cursor. Capped reads can
        // report closed while omitting chunks, and retained history can evict data.
        let response = process
            .read(Some(output.after()), None, Some(1000))
            .await
            .map_err(|_| OperationError::Unsettled)?;
        if response.failure.is_some() {
            return Err(OperationError::Unsettled);
        }
        let settled = response.closed && response.exited;
        if output.append(&response).is_err() {
            return if settled {
                Err(OperationError::Rejected("native_error"))
            } else {
                stop_and_reject(process).await
            };
        }
        if settled {
            if response.exit_code != Some(0) || response.sandbox_denied == Some(true) {
                return Err(OperationError::Rejected("native_error"));
            }
            return output::decode(&output.bytes, limit);
        }
    }
}

fn qualified_path(helper: &Path, workspace: &Path) -> bool {
    let Some(value) = helper.to_str() else {
        return false;
    };
    helper.is_absolute()
        && !helper.starts_with(workspace)
        && value
            .split('/')
            .skip(1)
            .all(|part| !part.is_empty() && part != "." && part != "..")
        && !value.contains(['\\', '\0', '\r', '\n'])
        && helper
            .components()
            .all(|part| matches!(part, Component::RootDir | Component::Normal(_)))
}

async fn stop_and_reject(process: &dyn ExecProcess) -> Result<Value, OperationError> {
    process
        .terminate()
        .await
        .map_err(|_| OperationError::Unsettled)?;
    loop {
        let response = process
            .read(None, None, Some(1000))
            .await
            .map_err(|_| OperationError::Unsettled)?;
        if response.failure.is_some() {
            return Err(OperationError::Unsettled);
        }
        // A terminate reply alone is not cleanup. The enclosing retained wait
        // bounds this drain and fails the owner if exit/output close stay unknown.
        if response.exited && response.closed {
            return Err(OperationError::Rejected("native_error"));
        }
    }
}
