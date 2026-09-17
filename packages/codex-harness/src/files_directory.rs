use super::{OperationError, file_error};
use codex_exec_server::{Environment, FileSystemSandboxContext, GetMetadataOptions, WalkOptions};
use codex_protocol::models::PermissionProfile;
use codex_protocol::permissions::{
    FileSystemAccessMode, FileSystemPath, FileSystemSandboxEntry, FileSystemSandboxPolicy,
    NetworkSandboxPolicy,
};
use codex_utils_path_uri::PathUri;
use serde_json::{Value, json};

pub(super) const MAX_ENTRIES: usize = 4096;

pub(super) async fn list(
    environment: &Environment,
    workspace: &PathUri,
    path: &PathUri,
    limit: usize,
) -> Result<Value, OperationError> {
    let policy = FileSystemSandboxPolicy::restricted(vec![FileSystemSandboxEntry::new(
        FileSystemPath::Path {
            path: workspace.clone(),
        },
        FileSystemAccessMode::Read,
    )]);
    let sandbox = FileSystemSandboxContext::from_permission_profile_with_cwd(
        PermissionProfile::from_runtime_permissions(&policy, NetworkSandboxPolicy::Restricted),
        workspace.clone(),
    );
    let filesystem = environment.get_filesystem();
    let no_follow = GetMetadataOptions {
        follow_symlinks: false,
    };
    let metadata = filesystem
        .get_metadata(path, no_follow, Some(&sandbox))
        .await
        .map_err(rejected)?;
    if metadata.is_symlink || !metadata.is_directory {
        return Err(OperationError::Rejected("invalid_path"));
    }
    // Use the native bounded walk at depth zero, without directory symlink traversal.
    // Its pinned implementation omits symlinks and non-regular entries; this private
    // observation alone cannot establish complete public Files type semantics.
    let outcome = filesystem
        .walk(
            path,
            WalkOptions {
                max_depth: 0,
                max_directories: 1,
                max_entries: limit,
                follow_directory_symlinks: false,
                prune_hidden_directories: false,
            },
            Some(&sandbox),
        )
        .await
        .map_err(rejected)?;
    if !outcome.errors.is_empty() {
        return Err(OperationError::Rejected("native_error"));
    }
    let root = path.to_abs_path().map_err(rejected)?;
    let mut entries = Vec::with_capacity(outcome.entries.len());
    for entry in outcome.entries {
        let absolute = entry.path.to_abs_path().map_err(rejected)?;
        let relative = absolute
            .as_path()
            .strip_prefix(root.as_path())
            .map_err(|_| OperationError::Rejected("native_error"))?;
        let name = relative
            .to_str()
            .filter(|name| {
                !name.is_empty()
                    && !name.contains(['/', '\\', '\0', '\r', '\n'])
                    && *name != "."
                    && *name != ".."
            })
            .ok_or(OperationError::Rejected("native_error"))?;
        let metadata = filesystem
            .get_metadata(&entry.path, no_follow, Some(&sandbox))
            .await
            .map_err(rejected)?;
        let (kind, size) = if metadata.is_symlink {
            ("symlink", None)
        } else if metadata.is_file {
            (
                "file",
                Some(
                    i64::try_from(metadata.size)
                        .map_err(|_| OperationError::Rejected("native_error"))?,
                ),
            )
        } else if metadata.is_directory {
            ("directory", None)
        } else {
            ("other", None)
        };
        entries.push(json!({"name":name,"kind":kind,"size_bytes":size}));
    }
    Ok(json!({"directory":{"entries":entries,"truncated":outcome.truncated}}))
}

fn rejected(error: std::io::Error) -> OperationError {
    OperationError::Rejected(file_error(error))
}
