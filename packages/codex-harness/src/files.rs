use anyhow::{Context, Result, ensure};
use codex_exec_server::{EnvironmentManager, EnvironmentObservedStatus, GetMetadataOptions};
use codex_utils_path_uri::PathUri;
use serde::Deserialize;
use serde_json::{Value, json};
use std::future::Future;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, PermissionsExt};
use std::path::{Component, Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
use tokio::net::{UnixListener, UnixStream};
use tokio::sync::oneshot;
use tokio::time::{Instant, timeout_at};
use tokio_util::sync::CancellationToken;

use crate::options::Binding;

const MAX_FRAME: usize = 8192;
const REQUEST_DEADLINE: Duration = Duration::from_secs(10);

pub struct PrivateSocket {
    listener: UnixListener,
    root: PathBuf,
    uid: u32,
}

impl PrivateSocket {
    pub fn bind(root: &Path) -> Result<Self> {
        let uid = std::fs::metadata("/proc/self")?.uid();
        let state = PathBuf::from(std::env::var_os("HOME").context("HOME is required")?)
            .join(".parsar")
            .canonicalize()?;
        let parent = root.parent().context("IPC parent is missing")?;
        let canonical = parent.canonicalize()?;
        ensure!(
            canonical == parent && parent.starts_with(&state),
            "IPC root must be below canonical ~/.parsar"
        );
        for ancestor in parent.ancestors() {
            let metadata = std::fs::symlink_metadata(ancestor)?;
            ensure!(
                metadata.is_dir()
                    && (metadata.uid() == uid || metadata.uid() == 0)
                    && metadata.mode() & 0o022 == 0,
                "IPC ancestors must be trusted directories"
            );
        }
        ensure!(
            root.join("files.sock").as_os_str().len() < 104,
            "IPC socket path is too long"
        );
        std::fs::DirBuilder::new()
            .mode(0o700)
            .create(root)
            .context("IPC root must be new")?;
        let listener = match UnixListener::bind(root.join("files.sock")) {
            Ok(listener) => listener,
            Err(error) => {
                let _ = std::fs::remove_dir(root);
                return Err(error).context("bind private metadata socket");
            }
        };
        let socket = Self {
            listener,
            root: root.to_owned(),
            uid,
        };
        std::fs::set_permissions(
            socket.root.join("files.sock"),
            std::fs::Permissions::from_mode(0o600),
        )?;
        Ok(socket)
    }

    pub async fn serve(
        &self,
        published: oneshot::Receiver<Arc<EnvironmentManager>>,
        binding: &Binding,
        stopping: &CancellationToken,
    ) -> Result<()> {
        let manager = tokio::select! {
            biased;
            _ = stopping.cancelled() => return Ok(()),
            result = published => result.context("native manager was not published")?,
        };
        loop {
            // A native deadline ends this owner: dropping a response future
            // does not settle the remote operation or authorize another one.
            let (stream, _) = tokio::select! {
                biased;
                _ = stopping.cancelled() => return Ok(()),
                result = self.listener.accept() => result?,
            };
            if stream.peer_cred()?.uid() != self.uid {
                continue;
            }
            if let Ok(outcome) = serve_connection(stream, &manager, binding, stopping).await {
                outcome.require_settled()?;
            }
        }
    }
}

impl Drop for PrivateSocket {
    fn drop(&mut self) {
        // Remove only this instance's known socket and empty private directory.
        let _ = std::fs::remove_file(self.root.join("files.sock"));
        let _ = std::fs::remove_dir(&self.root);
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    environment_id: String,
    path: String,
}

fn request_path(frame: &[u8], binding: &Binding) -> Result<PathUri, &'static str> {
    if frame.len() > MAX_FRAME || !frame.ends_with(b"\n") {
        return Err("invalid_request");
    }
    let request: Request = serde_json::from_slice(frame).map_err(|_| "invalid_request")?;
    if request.environment_id != binding.environment {
        return Err("wrong_environment");
    }
    let path = Path::new(&request.path);
    if request.path.is_empty()
        || request.path.contains(['\0', '\\'])
        || !path
            .components()
            .all(|part| matches!(part, Component::Normal(_)))
    {
        return Err("invalid_path");
    }
    PathUri::from_host_native_path(binding.workspace.join(path)).map_err(|_| "invalid_path")
}

async fn metadata(manager: &EnvironmentManager, path: PathUri) -> Result<Value, &'static str> {
    // The native manager key is distinct from the registry's Environment UUID;
    // startup validates that UUID against the frozen operator binding.
    let environment = manager
        .get_environment("remote")
        .ok_or("environment_unavailable")?;
    if manager.try_local_environment().is_some()
        || !environment.is_remote()
        || !matches!(environment.status().await, EnvironmentObservedStatus::Ready)
    {
        return Err("environment_unavailable");
    }
    // This private operator endpoint does not establish public path isolation.
    // Native parent-component traversal remains subject to deployment policy.
    let metadata = environment
        .get_filesystem()
        .get_metadata(
            &path,
            GetMetadataOptions {
                follow_symlinks: false,
            },
            None,
        )
        .await
        .map_err(|error| match error.kind() {
            std::io::ErrorKind::NotFound => "not_found",
            std::io::ErrorKind::PermissionDenied => "permission_denied",
            _ => "native_error",
        })?;
    Ok(
        json!({"size":metadata.size,"is_file":metadata.is_file,"is_directory":metadata.is_directory,"is_symlink":metadata.is_symlink,"created_at_ms":metadata.created_at_ms,"modified_at_ms":metadata.modified_at_ms}),
    )
}

#[derive(Debug, PartialEq)]
enum ConnectionOutcome {
    Settled,
    UnsettledNativeOperation,
}

impl ConnectionOutcome {
    fn require_settled(self) -> Result<()> {
        ensure!(
            self == Self::Settled,
            "native metadata deadline expired; owner stopped with remote operation unresolved"
        );
        Ok(())
    }
}

async fn serve_connection(
    stream: UnixStream,
    manager: &EnvironmentManager,
    binding: &Binding,
    stopping: &CancellationToken,
) -> Result<ConnectionOutcome> {
    exchange(stream, binding, REQUEST_DEADLINE, stopping, |path| {
        metadata(manager, path)
    })
    .await
}

async fn exchange<F, R>(
    stream: UnixStream,
    binding: &Binding,
    duration: Duration,
    stopping: &CancellationToken,
    operation: F,
) -> Result<ConnectionOutcome>
where
    F: FnOnce(PathUri) -> R,
    R: Future<Output = Result<Value, &'static str>>,
{
    let deadline = Instant::now() + duration;
    let (read, mut write) = stream.into_split();
    let mut reader = BufReader::new(read.take((MAX_FRAME + 1) as u64));
    let mut frame = Vec::new();
    let frame_result = tokio::select! {
        biased;
        _ = stopping.cancelled() => return Ok(ConnectionOutcome::Settled),
        result = timeout_at(deadline, reader.read_until(b'\n', &mut frame)) => result,
    };
    match frame_result {
        Ok(result) => {
            result?;
        }
        Err(_) => return Ok(ConnectionOutcome::Settled),
    }
    // From admission until the native response/deadline, only this owner holds
    // the operation. Caller disconnect and runner shutdown do not drop its wait.
    let result = match request_path(&frame, binding) {
        Ok(path) => match timeout_at(deadline, operation(path)).await {
            Ok(result) => result,
            Err(_) => return Ok(ConnectionOutcome::UnsettledNativeOperation),
        },
        Err(error) => Err(error),
    };
    let response = match result {
        Ok(value) => json!({"metadata":value}),
        Err(error) => json!({"error":error}),
    };
    let mut bytes = serde_json::to_vec(&response)?;
    bytes.push(b'\n');
    // The native future returned. Response delivery no longer owns that wait;
    // a transport error still cannot establish remote operation retirement.
    tokio::select! {
        biased;
        _ = stopping.cancelled() => {},
        _ = timeout_at(deadline, async {
            write.write_all(&bytes).await?;
            write.shutdown().await
        }) => {},
    }
    Ok(ConnectionOutcome::Settled)
}

#[cfg(test)]
#[path = "files_tests.rs"]
mod tests;
