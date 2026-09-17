use super::*;

fn binding() -> Binding {
    Binding {
        native_binary: "/native".into(),
        environment: "expected".into(),
        workspace: "/workspace".into(),
        ipc_root: "/unused".into(),
    }
}

#[test]
fn read_requires_an_explicit_bounded_limit() {
    let binding = binding();
    for fields in [
        json!({"operation":"read"}),
        json!({"operation":"read","max_bytes":null}),
        json!({"operation":"read","max_bytes":0}),
        json!({"operation":"read","max_bytes":-1}),
        json!({"operation":"read","max_bytes":MAX_BOUNDED_FILE_READ_BYTES + 1}),
        json!({"operation":"read","max_bytes":1.5}),
        json!({"operation":"write","max_bytes":1}),
        json!({"max_bytes":1}),
    ] {
        let mut frame = json!({"environment_id":"expected","path":"file"});
        frame
            .as_object_mut()
            .expect("object")
            .extend(fields.as_object().expect("fields").clone());
        assert!(request_command(format!("{frame}\n").as_bytes(), &binding).is_err());
    }
    for limit in [1, MAX_BOUNDED_FILE_READ_BYTES] {
        let frame =
            json!({"environment_id":"expected","path":"file","operation":"read","max_bytes":limit});
        let command =
            request_command(format!("{frame}\n").as_bytes(), &binding).expect("valid read");
        assert_eq!(command.read_limit, Some(limit));
    }
}

#[tokio::test]
async fn read_wait_includes_close_after_caller_detaches() -> Result<()> {
    let binding = binding();
    let stopping = CancellationToken::new();
    let (server, mut client) = UnixStream::pair()?;
    client.write_all(b"{\"environment_id\":\"expected\",\"path\":\"file\",\"operation\":\"read\",\"max_bytes\":4}\n").await?;
    let (admitted, admission) = oneshot::channel();
    let (read_done, read_result) = oneshot::channel();
    let (closing, close_started) = oneshot::channel();
    let (close_done, close_result) = oneshot::channel();
    let files = exchange(
        server,
        &binding,
        REQUEST_DEADLINE,
        &stopping,
        |command| async move {
            assert_eq!(command.read_limit, Some(4));
            admitted.send(()).expect("admission observed");
            read_result.await.expect("read wait retained");
            closing.send(()).expect("close observed");
            close_result.await.expect("close wait retained")
        },
    );
    tokio::pin!(files);
    tokio::select! {
        result = &mut files => panic!("read ended before admission: {result:?}"),
        result = admission => result?,
    }
    drop(client);
    stopping.cancel();
    read_done.send(()).expect("read wait survives detach");
    tokio::select! {
        result = &mut files => panic!("read ended before close: {result:?}"),
        result = close_started => result?,
    }
    assert!(futures::poll!(&mut files).is_pending());
    close_done
        .send(Err(OperationError::Unsettled))
        .expect("close wait survives stop");
    let outcome = files.await?;
    assert_eq!(outcome, ConnectionOutcome::UnsettledNativeOperation);
    assert!(outcome.require_settled().is_err());
    Ok(())
}

#[tokio::test]
async fn unconfirmed_read_or_close_never_delivers_a_success_response() -> Result<()> {
    let binding = binding();
    let stopping = CancellationToken::new();
    let (server, mut client) = UnixStream::pair()?;
    client.write_all(b"{\"environment_id\":\"expected\",\"path\":\"file\",\"operation\":\"read\",\"max_bytes\":1}\n").await?;
    let outcome = exchange(server, &binding, REQUEST_DEADLINE, &stopping, |_| async {
        Err(OperationError::Unsettled)
    })
    .await?;
    assert_eq!(outcome, ConnectionOutcome::UnsettledNativeOperation);
    assert_eq!(client.read(&mut [0; 1]).await?, 0);
    Ok(())
}
