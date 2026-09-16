#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires the task-owned Docker placement runner"]
async fn native_placement_worker() {
    let root = std::path::PathBuf::from(
        std::env::var_os("PARSAR_RETIREMENT_WORKSPACE").expect("qualification workspace"),
    );
    let home = std::path::PathBuf::from(std::env::var_os("HOME").expect("private HOME"));
    assert!(root.is_absolute() && root.starts_with(home.join(".parsar")));
    let mode = std::env::var("PARSAR_RETIREMENT_MODE").expect("qualification mode");
    assert!(mode == "held" || mode == "successor");
    let file = root.join("write.bin");
    let processor = super::super::ConnectionProcessor::new(test_runtime_paths());
    let (mut writer, mut lines, handler) =
        spawn_test_connection(Arc::clone(&processor.session_registry), &mode);
    initialize(&mut writer, &mut lines, None).await;
    if mode == "successor" {
        send_request(
            &mut writer,
            2,
            FS_WRITE_FILE_METHOD,
            &write_params(&file, b"new-owner"),
        )
        .await;
        let _: FsWriteFileResponse = read_response(&mut lines, 2).await;
        assert_eq!(std::fs::read(&file).expect("successor bytes"), b"new-owner");
        std::fs::write(root.join("successor-receipt"), b"acknowledged").expect("receipt marker");
        drop(writer);
        handler.await.expect("successor handler");
        processor.shutdown().await;
        return;
    }
    assert_eq!(std::fs::read(&file).expect("initial bytes"), b"initial");
    let mut command = exec_params(ProcessId::from("placement-command"));
    command.cwd = PathUri::from_host_native_path(&root).expect("workspace URI");
    command.argv = vec!["/bin/sh".into(), "-c".into(),
            "setsid /bin/sh -c 'echo $$ > descendant.pid; i=0; while [ \"$i\" -lt 1000 ]; do printf x >> descendant-heartbeat; sleep 0.02; i=$((i+1)); done' </dev/null >/dev/null 2>&1 & echo $$ > command.pid; i=0; while [ \"$i\" -lt 1000 ]; do printf x >> heartbeat; sleep 0.02; i=$((i+1)); done".into()];
    send_request(&mut writer, 2, EXEC_METHOD, &command).await;
    let _: ExecResponse = read_response(&mut lines, 2).await;
    wait_for(|| {
        root.join("descendant.pid").exists()
            && heartbeat(&root) > 0
            && std::fs::metadata(root.join("descendant-heartbeat")).is_ok_and(|m| m.len() > 0)
    })
    .await;
    let gate = Gate::arm(file.clone());
    send_request(
        &mut writer,
        3,
        FS_WRITE_FILE_METHOD,
        &write_params(&file, b"old-owner"),
    )
    .await;
    wait_for(|| gate.0.entered.load(Ordering::SeqCst)).await;
    std::fs::write(root.join("worker-entered"), b"held-before-truncate").expect("ready marker");
    // The external supervisor must retire this placement while the original
    // blocking worker still owns its descriptor; returning would release it.
    tokio::time::sleep(Duration::from_secs(12)).await;
    panic!("placement was not retired before the qualification deadline");
}
