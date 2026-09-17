use super::*;

#[tokio::test]
async fn runner_completion_keeps_the_original_admitted_deadline() -> Result<()> {
    let binding = Binding {
        native_binary: "/native".into(),
        environment: "expected".into(),
        workspace: "/workspace".into(),
        ipc_root: "/unused".into(),
    };
    let stopping = CancellationToken::new();
    let (server, mut client) = UnixStream::pair()?;
    client
        .write_all(b"{\"environment_id\":\"expected\",\"path\":\"file\"}\n")
        .await?;
    // Establish actual socket readiness before using the controlled clock.
    server.readable().await?;
    tokio::time::pause();
    let (admitted, mut admission) = oneshot::channel();
    let (complete, completion) = oneshot::channel();
    let (finish_runner, runner_done) = oneshot::channel();
    let files = async {
        exchange(
            server,
            &binding,
            REQUEST_DEADLINE,
            &stopping,
            |_path| async {
                admitted.send(()).expect("observe admission");
                completion.await.expect("retain native response")
            },
        )
        .await?
        .require_settled()
    };
    let operation = crate::owner::supervise(
        async { runner_done.await.map_err(Into::into) },
        files,
        &stopping,
    );
    tokio::pin!(operation);
    let started = Instant::now();
    assert!(futures::poll!(&mut operation).is_pending());
    admission.try_recv()?;
    tokio::time::advance(Duration::from_secs(6)).await;
    finish_runner.send(()).expect("runner still owned");
    assert!(futures::poll!(&mut operation).is_pending());
    assert!(stopping.is_cancelled());
    assert!(!complete.is_closed());
    tokio::time::advance(Duration::from_millis(3999)).await;
    assert!(futures::poll!(&mut operation).is_pending());
    tokio::time::advance(Duration::from_millis(1)).await;
    let error = operation
        .await
        .expect_err("original deadline must fail the owner");
    // Tokio's timer wheel may round the original deadline up by one millisecond.
    assert!(
        (REQUEST_DEADLINE..=REQUEST_DEADLINE + Duration::from_millis(1))
            .contains(&(Instant::now() - started))
    );
    assert_eq!(error.to_string(), "private file operation did not drain");
    assert!(error.root_cause().to_string().contains("deadline expired"));
    assert!(complete.send(Ok(json!({"size": 42}))).is_err());
    Ok(())
}

#[tokio::test]
async fn admitted_operation_survives_caller_detach_and_owner_stop() -> Result<()> {
    for stop_owner in [false, true] {
        let binding = Binding {
            native_binary: "/native".into(),
            environment: "expected".into(),
            workspace: "/workspace".into(),
            ipc_root: "/unused".into(),
        };
        let stopping = CancellationToken::new();
        let (server, mut client) = UnixStream::pair()?;
        client
            .write_all(b"{\"environment_id\":\"expected\",\"path\":\"file\"}\n")
            .await?;
        let (admitted, admission) = oneshot::channel();
        let (complete, completion) = oneshot::channel();
        let operation = exchange(
            server,
            &binding,
            Duration::from_secs(1),
            &stopping,
            |_path| async {
                admitted.send(()).expect("observe admission");
                completion.await.expect("owned native response")
            },
        );
        tokio::pin!(operation);
        // Poll actual admission before dropping the caller, without a sleep.
        tokio::select! {
            result = &mut operation => panic!("operation ended before native reply: {result:?}"),
            result = admission => result?,
        }
        drop(client);
        if stop_owner {
            stopping.cancel();
        }
        assert!(futures::poll!(&mut operation).is_pending());
        complete
            .send(Ok(json!({"size": 42})))
            .expect("caller detach must retain native wait");
        operation.await?.require_settled()?;
    }
    Ok(())
}

#[tokio::test]
async fn stopped_owner_does_not_admit_even_a_complete_frame() -> Result<()> {
    let binding = Binding {
        native_binary: "/native".into(),
        environment: "expected".into(),
        workspace: "/workspace".into(),
        ipc_root: "/unused".into(),
    };
    let stopping = CancellationToken::new();
    stopping.cancel();
    let (server, mut client) = UnixStream::pair()?;
    client
        .write_all(b"{\"environment_id\":\"expected\",\"path\":\"file\"}\n")
        .await?;
    exchange(
        server,
        &binding,
        REQUEST_DEADLINE,
        &stopping,
        |_path| async { panic!("stopped owner must not admit native work") },
    )
    .await?
    .require_settled()?;
    drop(client);
    Ok(())
}

#[tokio::test]
async fn private_socket_collision_never_removes_the_original() -> Result<()> {
    let state = PathBuf::from(std::env::var_os("HOME").context("HOME missing")?).join(".parsar");
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
        let stopping = CancellationToken::new();
        let (_, response) = tokio::try_join!(
            serve_connection(server, &manager, &binding, &stopping),
            exchange
        )?;
        assert_eq!(response, json!({"error":expected}));
    }
    let (server, mut client) = UnixStream::pair()?;
    assert!(
        tokio::time::timeout(
            Duration::from_millis(20),
            serve_connection(server, &manager, &binding, &CancellationToken::new())
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
    let outcome = exchange(
        server,
        &binding,
        Duration::from_millis(20),
        &CancellationToken::new(),
        |_path| async {
            dispatched.send(()).unwrap();
            // The remote side has accepted work but has not settled its reply.
            received.await.unwrap()
        },
    )
    .await?;
    observed.await?;
    assert_eq!(outcome, ConnectionOutcome::UnsettledNativeOperation);
    assert!(outcome.require_settled().is_err());
    assert_eq!(client.read(&mut [0; 1]).await?, 0);
    // A response producer can still complete after its receiver was dropped;
    // this is why timeout must fail the owner rather than release admission.
    assert!(response.send(Ok(json!({"size": 1}))).is_err());

    let (server, mut client) = UnixStream::pair()?;
    let outcome = exchange(
        server,
        &binding,
        Duration::from_millis(20),
        &CancellationToken::new(),
        |_path| async { panic!("a stalled frame must not dispatch native work") },
    )
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
