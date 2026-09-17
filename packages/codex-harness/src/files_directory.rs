use super::OperationError;
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
    let root = workspace.to_abs_path().map_err(operation_error)?;
    let target = path.to_abs_path().map_err(operation_error)?;
    let relative = target
        .as_path()
        .strip_prefix(root.as_path())
        .map_err(|_| OperationError::Rejected("invalid_path"))?;
    let mut parent = root.as_path().to_path_buf();
    // Reject symlink ancestors before native traversal can erase its typed error.
    for component in relative
        .components()
        .take(relative.components().count().saturating_sub(1))
    {
        parent.push(component);
        let parent_path = PathUri::from_host_native_path(&parent).map_err(operation_error)?;
        let metadata = filesystem
            .get_metadata(&parent_path, no_follow, Some(&sandbox))
            .await
            .map_err(operation_error)?;
        if metadata.is_symlink || !metadata.is_directory {
            return Err(OperationError::Rejected("invalid_path"));
        }
    }
    let metadata = filesystem
        .get_metadata(path, no_follow, Some(&sandbox))
        .await
        .map_err(operation_error)?;
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
        .map_err(operation_error)?;
    if !outcome.errors.is_empty() {
        return Err(OperationError::Rejected("native_error"));
    }
    let root = path.to_abs_path().map_err(operation_error)?;
    let mut entries = Vec::with_capacity(outcome.entries.len());
    for entry in outcome.entries {
        let absolute = entry.path.to_abs_path().map_err(operation_error)?;
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
            .map_err(operation_error)?;
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

pub(super) fn operation_error(error: std::io::Error) -> OperationError {
    match error.kind() {
        std::io::ErrorKind::NotFound => OperationError::Rejected("not_found"),
        std::io::ErrorKind::PermissionDenied => OperationError::Rejected("permission_denied"),
        std::io::ErrorKind::InvalidInput => OperationError::Rejected("invalid_path"),
        // Native transport errors do not confirm remote operation cleanup.
        _ => OperationError::Unsettled,
    }
}
