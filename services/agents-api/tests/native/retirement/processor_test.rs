mod retirement_qualification {
    use super::*;
    use crate::protocol::{FS_WRITE_FILE_METHOD, FsWriteFileParams, FsWriteFileResponse};
    use crate::retirement_gate::Gate;
    use base64::Engine;
    use base64::prelude::BASE64_STANDARD;
    use pretty_assertions::assert_eq;
    use std::path::Path;
    use std::sync::atomic::Ordering;

    // This is a negative qualification: native detach/shutdown is not a Files
    // retirement receipt. The original blocking write is never retried.
    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn native_retirement_does_not_settle_admitted_write() {
        for shutdown in [false, true] {
            qualify(shutdown).await;
        }
    }

    async fn qualify(shutdown: bool) {
        let private = std::path::PathBuf::from(std::env::var_os("HOME").expect("caller HOME"))
            .join(".parsar")
            .canonicalize()
            .expect("private state root");
        let temporary =
            std::path::PathBuf::from(std::env::var_os("TMPDIR").expect("private TMPDIR"))
                .canonicalize()
                .expect("temporary state root");
        assert!(
            temporary.starts_with(private),
            "TMPDIR must be below ~/.parsar"
        );
        let root = tempfile::Builder::new()
            .prefix("retirement-")
            .tempdir_in(temporary)
            .expect("private workspace");
        let started = std::time::Instant::now();
        let mut timeline = Vec::new();
        let mut observed = |event: &str| {
            timeline.push(serde_json::json!({
                "event": event, "elapsed_us": started.elapsed().as_micros(),
            }))
        };
        let file = root.path().join("write.bin");
        std::fs::write(&file, b"initial").expect("initial file");
        let processor = super::super::ConnectionProcessor::new(test_runtime_paths());
        let registry = Arc::clone(&processor.session_registry);
        let (mut writer, mut lines, first) = spawn_test_connection(Arc::clone(&registry), "old");
        initialize(&mut writer, &mut lines, None).await;

        let mut command = exec_params(ProcessId::from("retirement-command"));
        command.cwd = PathUri::from_host_native_path(root.path()).expect("workspace URI");
        command.argv = vec![
            "/bin/sh".into(), "-c".into(),
            "echo $$ > command.pid; i=0; while [ \"$i\" -lt 1000 ]; do printf x >> heartbeat; sleep 0.02; i=$((i+1)); done".into(),
        ];
        send_request(&mut writer, 2, EXEC_METHOD, &command).await;
        let _: ExecResponse = read_response(&mut lines, 2).await;
        wait_for(|| root.path().join("command.pid").exists() && heartbeat(root.path()) > 0).await;
        let pid: u32 = std::fs::read_to_string(root.path().join("command.pid"))
            .expect("command pid")
            .trim()
            .parse()
            .expect("numeric pid");
        let process = format!("/proc/{pid}");
        assert!(Path::new(&process).exists(), "owned command must exist");

        let gate = Gate::arm(file.clone());
        send_request(
            &mut writer,
            3,
            FS_WRITE_FILE_METHOD,
            &write_params(&file, b"old-owner"),
        )
        .await;
        wait_for(|| gate.0.entered.load(Ordering::SeqCst)).await;
        observed("original_blocking_worker_entered");
        assert_eq!(std::fs::read(&file).expect("held bytes"), b"initial");
        drop(writer);
        timeout(Duration::from_secs(2), first)
            .await
            .expect("disconnected handler should return")
            .expect("handler join");
        observed("connection_handler_returned");
        assert!(
            lines
                .next_line()
                .await
                .expect("old response stream")
                .is_none(),
            "held mutation must have no response"
        );
        assert!(!gate.0.finished.load(Ordering::SeqCst));
        let before = heartbeat(root.path());
        wait_for(|| heartbeat(root.path()) > before).await;

        if shutdown {
            timeout(Duration::from_secs(2), processor.shutdown())
                .await
                .expect("native processor shutdown should return");
            observed("native_processor_shutdown_returned");
            wait_for(|| !Path::new(&process).exists()).await;
            observed("owned_command_exit_observed");
            assert!(
                !gate.0.finished.load(Ordering::SeqCst),
                "local command retirement is distinct from the admitted file worker"
            );
        }

        // A new native owner for the same path can be admitted while the old
        // blocking worker still holds its descriptor. No Core policy is implied.
        let successor = super::super::ConnectionProcessor::new(test_runtime_paths());
        let (mut next_writer, mut next_lines, next) =
            spawn_test_connection(Arc::clone(&successor.session_registry), "successor");
        initialize(&mut next_writer, &mut next_lines, None).await;
        send_request(
            &mut next_writer,
            2,
            FS_WRITE_FILE_METHOD,
            &write_params(&file, b"new-owner"),
        )
        .await;
        let _: FsWriteFileResponse =
            timeout(Duration::from_secs(2), read_response(&mut next_lines, 2))
                .await
                .expect("successor write receipt");
        assert_eq!(std::fs::read(&file).expect("successor bytes"), b"new-owner");
        assert!(!gate.0.finished.load(Ordering::SeqCst));
        observed("successor_write_acknowledged_and_bytes_verified");
        gate.0.release();
        wait_for(|| gate.0.finished.load(Ordering::SeqCst)).await;
        assert_eq!(std::fs::read(&file).expect("late bytes"), b"old-owner");
        observed("original_worker_finished_and_overwrote_successor");

        drop(next_writer);
        drop(next_lines);
        timeout(Duration::from_secs(2), next)
            .await
            .expect("successor shutdown")
            .expect("successor join");
        successor.shutdown().await;
        processor.shutdown().await;
        wait_for(|| !Path::new(&process).exists()).await;
        let stopped = heartbeat(root.path());
        tokio::time::sleep(Duration::from_millis(100)).await;
        assert_eq!(heartbeat(root.path()), stopped, "owned heartbeat stopped");
        println!(
            "{}",
            serde_json::json!({
                "qualification": "native-retirement", "instrumented": true,
                "boundary": if shutdown { "processor-shutdown" } else { "connection-detach" },
                "write_dispatched_and_worker_entered": true, "old_receipt": "unknown",
                "handler_returned_while_write_held": true,
                "command_continued_after_detach": true,
                "processor_shutdown_before_successor": shutdown,
                "successor_receipt_before_old_completion": true,
                "bytes": ["initial", "new-owner", "old-owner"],
            "owned_command_exit_observed": true, "retirement_barrier": false,
            "timeline": timeline,
            })
        );
    }

    async fn initialize(
        writer: &mut DuplexStream,
        lines: &mut Lines<BufReader<DuplexStream>>,
        resume: Option<String>,
    ) {
        send_request(
            writer,
            1,
            INITIALIZE_METHOD,
            &InitializeParams {
                client_name: "retirement-qualification".into(),
                resume_session_id: resume,
            },
        )
        .await;
        let _: InitializeResponse = read_response(lines, 1).await;
        send_notification(writer, INITIALIZED_METHOD, &()).await;
    }

    fn write_params(path: &Path, data: &[u8]) -> FsWriteFileParams {
        FsWriteFileParams {
            path: PathUri::from_host_native_path(path).expect("file URI"),
            data_base64: BASE64_STANDARD.encode(data),
            follow_symlinks: Some(false),
            sandbox: None,
        }
    }

    fn heartbeat(root: &Path) -> u64 {
        std::fs::metadata(root.join("heartbeat"))
            .map(|m| m.len())
            .unwrap_or(0)
    }

    async fn wait_for(mut predicate: impl FnMut() -> bool) {
        timeout(Duration::from_secs(2), async {
            while !predicate() {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .expect("bounded observation");
    }
    include!("placement_qualification.rs");
}
