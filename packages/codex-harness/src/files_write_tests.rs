use super::*;
use crate::options::WriteBinding;

fn binding() -> Binding {
    let workspace = PathBuf::from("/data/workspace");
    Binding {
        write: WriteBinding::from_paths(
            &workspace,
            Some("/bin/helper".into()),
            Some("/data/staging".into()),
        )
        .unwrap(),
        directory_helper: None,
        native_binary: "/native".into(),
        environment: "expected".into(),
        workspace,
        ipc_root: "/unused".into(),
    }
}
fn frame(path: &str, size: usize) -> Vec<u8> {
    format!(
        "{}\n",
        json!({"environment_id":"expected","path":path,"operation":"write","size_bytes":size})
    )
    .into_bytes()
}

#[test]
fn write_is_opt_in_and_never_admitted_by_read_only_preparation() {
    let mut binding = binding();
    for size in [0, write::MAX_BYTES] {
        assert!(request_command(&frame("a/b", size), &binding).is_ok());
    }
    assert!(request_command(&frame("file", write::MAX_BYTES + 1), &binding).is_err());
    for path in ["", "/file", "a//b", "a/./b", "../b", "a/", "a\nb"] {
        assert!(request_command(&frame(path, 0), &binding).is_err());
    }
    for extra in [
        json!({"max_bytes":1}),
        json!({"max_entries":1}),
        json!({"size_bytes":null}),
        json!({"size_bytes":-1}),
    ] {
        let mut value: Value = serde_json::from_slice(&frame("a", 0)).unwrap();
        value
            .as_object_mut()
            .unwrap()
            .extend(extra.as_object().unwrap().clone());
        assert!(request_command(format!("{value}\n").as_bytes(), &binding).is_err());
    }
    binding.restrict_reads(true);
    assert!(matches!(
        request_command(&frame("file", 0), &binding),
        Err("unsupported")
    ));
}

#[tokio::test]
async fn body_must_be_complete_before_any_native_dispatch() -> Result<()> {
    let binding = binding();
    for (declared, payload) in [
        (4, b"abc".as_slice()),
        (write::MAX_BYTES + 1, b"".as_slice()),
    ] {
        let (server, mut client) = UnixStream::pair()?;
        client.write_all(&frame("file", declared)).await?;
        client.write_all(payload).await?;
        client.shutdown().await?;
        assert_eq!(
            exchange(
                server,
                &binding,
                REQUEST_DEADLINE,
                &CancellationToken::new(),
                |_| async { panic!("incomplete input reached native execution") }
            )
            .await?,
            ConnectionOutcome::Settled
        );
    }
    Ok(())
}

#[tokio::test]
async fn coalesced_header_and_binary_body_are_preserved_and_detach_retains_wait() -> Result<()> {
    for size in [0, 256 * 1024 + 3] {
        let binding = binding();
        let stopping = CancellationToken::new();
        let (server, mut client) = UnixStream::pair()?;
        let bytes: Vec<_> = (0..size).map(|n| (n % 256) as u8).collect();
        let mut input = frame("binary", size);
        input.extend(&bytes);
        let send = tokio::spawn(async move {
            client.write_all(&input).await?;
            Ok::<_, std::io::Error>(client)
        });
        let (admitted, admission) = oneshot::channel();
        let (complete, completion) = oneshot::channel();
        let operation = exchange(
            server,
            &binding,
            REQUEST_DEADLINE,
            &stopping,
            |command| async move {
                let upload = command.write.expect("write");
                assert_eq!(upload.bytes, bytes);
                admitted.send(()).unwrap();
                completion.await.unwrap()
            },
        );
        tokio::pin!(operation);
        tokio::select! { result = &mut operation => panic!("premature: {result:?}"), result = admission => result? }
        drop(send.await??);
        stopping.cancel();
        assert!(futures::poll!(&mut operation).is_pending());
        complete.send(Err(OperationError::Unsettled)).unwrap();
        assert_eq!(
            operation.await?,
            ConnectionOutcome::UnsettledNativeOperation
        );
    }
    Ok(())
}

#[tokio::test]
async fn body_shutdown_is_safe_but_native_deadline_is_unresolved() -> Result<()> {
    for admitted in [false, true] {
        let binding = binding();
        let stopping = CancellationToken::new();
        let (server, mut client) = UnixStream::pair()?;
        client
            .write_all(&frame("pending", if admitted { 0 } else { 1 }))
            .await?;
        server.readable().await?;
        let (started, start) = oneshot::channel();
        let duration = Duration::from_millis(10);
        let operation = exchange(server, &binding, duration, &stopping, |_| async move {
            started.send(()).unwrap();
            std::future::pending::<Result<Value, OperationError>>().await
        });
        tokio::pin!(operation);
        if admitted {
            tokio::select! { result = &mut operation => panic!("premature: {result:?}"), result = start => result? }
            stopping.cancel();
            assert_eq!(
                operation.await?,
                ConnectionOutcome::UnsettledNativeOperation
            );
        } else {
            assert!(futures::poll!(&mut operation).is_pending());
            stopping.cancel();
            assert_eq!(operation.await?, ConnectionOutcome::Settled);
        }
    }
    Ok(())
}
