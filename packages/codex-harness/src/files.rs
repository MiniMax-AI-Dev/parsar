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
    ) -> Result<()> {
        let manager = published
            .await
            .context("native manager was not published")?;
        loop {
            // A native deadline ends this owner: dropping a response future
            // does not settle the remote operation or authorize another one.
            let (stream, _) = self.listener.accept().await?;
            if stream.peer_cred()?.uid() != self.uid {
                continue;
            }
            if let Ok(outcome) = serve_connection(stream, &manager, binding).await {
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
) -> Result<ConnectionOutcome> {
    exchange(stream, binding, REQUEST_DEADLINE, |path| {
        metadata(manager, path)
    })
    .await
}

async fn exchange<F, R>(
    stream: UnixStream,
    binding: &Binding,
    duration: Duration,
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
    match timeout_at(deadline, reader.read_until(b'\n', &mut frame)).await {
        Ok(result) => {
            result?;
        }
        Err(_) => return Ok(ConnectionOutcome::Settled),
    }
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
    // The native result has settled. A failed or stalled response writer can
    // close this connection without leaving another native operation outstanding.
    let _ = timeout_at(deadline, async {
        write.write_all(&bytes).await?;
        write.shutdown().await
    })
    .await;
    Ok(ConnectionOutcome::Settled)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn private_socket_collision_never_removes_the_original() -> Result<()> {
        let state =
            PathBuf::from(std::env::var_os("HOME").context("HOME missing")?).join(".parsar");
        let parent = tempfile::Builder::new().prefix("hm-").tempdir_in(state)?;
        std::fs::set_permissions(parent.path(), std::fs::Permissions::from_mode(0o700))?;
        let root = parent.path().join("owner");
        let socket = PrivateSocket::bind(&root)?;
        assert_eq!(std::fs::metadata(&root)?.mode() & 0o777, 0o700);
        assert_eq!(
            std::fs::metadata(root.join("files.sock"))?.mode() & 0o777,
            0o600
        );
        assert!(PrivateSocket::bind(&root).is_err());
        let client = UnixStream::connect(root.join("files.sock")).await?;
        let (server, _) = socket.listener.accept().await?;
        assert_eq!(server.peer_cred()?.uid(), socket.uid);
        drop((client, server, socket));
        assert!(!root.exists());
        Ok(())
    }

    #[tokio::test]
    async fn invalid_requests_and_local_manager_never_reach_host_metadata() -> Result<()> {
        let manager = EnvironmentManager::default_for_tests();
        let binding = Binding {
            native_binary: "/native".into(),
            environment: "expected".into(),
            workspace: "/".into(),
            ipc_root: "/unused".into(),
        };
        for (request, expected) in [
            (
                b"{\"environment_id\":\"expected\",\"path\":\"etc/passwd\"}\n".to_vec(),
                "environment_unavailable",
            ),
            (
                b"{\"environment_id\":\"wrong\",\"path\":\"etc/passwd\"}\n".to_vec(),
                "wrong_environment",
            ),
            (vec![b'x'; MAX_FRAME + 1], "invalid_request"),
        ] {
            let (server, mut client) = UnixStream::pair()?;
            let exchange = async {
                client.write_all(&request).await?;
                let mut response = Vec::new();
                client.read_to_end(&mut response).await?;
                anyhow::Ok(serde_json::from_slice::<Value>(&response)?)
            };
            let (_, response) =
                tokio::try_join!(serve_connection(server, &manager, &binding), exchange)?;
            assert_eq!(response, json!({"error":expected}));
        }
        let (server, mut client) = UnixStream::pair()?;
        assert!(
            tokio::time::timeout(
                Duration::from_millis(20),
                serve_connection(server, &manager, &binding)
            )
            .await
            .is_err()
        );
        assert_eq!(client.read(&mut [0; 1]).await?, 0);
        Ok(())
    }

    #[tokio::test]
    async fn pending_native_response_requires_owner_failure() -> Result<()> {
        let binding = Binding {
            native_binary: "/native".into(),
            environment: "expected".into(),
            workspace: "/workspace".into(),
            ipc_root: "/unused".into(),
        };
        let (server, mut client) = UnixStream::pair()?;
        client
            .write_all(b"{\"environment_id\":\"expected\",\"path\":\"file\"}\n")
            .await?;
        let (dispatched, observed) = oneshot::channel();
        let (response, received) = oneshot::channel();
        let outcome = exchange(server, &binding, Duration::from_millis(20), |_path| async {
            dispatched.send(()).unwrap();
            // The remote side has accepted work but has not settled its reply.
            received.await.unwrap()
        })
        .await?;
        observed.await?;
        assert_eq!(outcome, ConnectionOutcome::UnsettledNativeOperation);
        assert!(outcome.require_settled().is_err());
        assert_eq!(client.read(&mut [0; 1]).await?, 0);
        // A response producer can still complete after its receiver was dropped;
        // this is why timeout must fail the owner rather than release admission.
        assert!(response.send(Ok(json!({"size": 1}))).is_err());

        let (server, mut client) = UnixStream::pair()?;
        let outcome = exchange(server, &binding, Duration::from_millis(20), |_path| async {
            panic!("a stalled frame must not dispatch native work")
        })
        .await?;
        outcome.require_settled()?;
        assert_eq!(client.read(&mut [0; 1]).await?, 0);
        Ok(())
    }

    #[test]
    fn rejects_wrong_identity_and_nonrelative_paths() {
        let binding = Binding {
            native_binary: "/native".into(),
            environment: "expected".into(),
            workspace: "/workspace".into(),
            ipc_root: "/unused".into(),
        };
        for path in ["", "/etc/passwd", "../file", "a/../file", "a\\b"] {
            let frame = format!("{}\n", json!({"environment_id":"expected","path":path}));
            assert!(request_path(frame.as_bytes(), &binding).is_err());
        }
        assert!(
            request_path(
                b"{\"environment_id\":\"wrong\",\"path\":\"file\"}\n",
                &binding
            )
            .is_err()
        );
        assert!(
            request_path(
                b"{\"environment_id\":\"expected\",\"path\":\"file\"}\n",
                &binding
            )
            .is_ok()
        );
        assert!(request_path(&vec![b'x'; MAX_FRAME + 1], &binding).is_err());
    }
}
