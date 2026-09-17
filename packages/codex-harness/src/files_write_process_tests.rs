use super::*;
use codex_exec_server::{
    ExecOutputStream, ExecProcessEventReceiver, ExecProcessFuture, ProcessOutputChunk,
    ProcessSignal, ReadResponse, WriteResponse,
};
use std::sync::{
    Mutex,
    atomic::{AtomicBool, Ordering},
};
use tokio::sync::watch;

struct Process {
    id: ProcessId,
    chunks: Mutex<Vec<Vec<u8>>>,
    reject: bool,
    receipt: Vec<u8>,
    terminated: AtomicBool,
}
impl Process {
    fn new(receipt: &[u8]) -> Self {
        Self {
            id: ProcessId::from("test"),
            chunks: Mutex::new(Vec::new()),
            reject: false,
            receipt: receipt.into(),
            terminated: AtomicBool::new(false),
        }
    }
}
impl ExecProcess for Process {
    fn process_id(&self) -> &ProcessId {
        &self.id
    }
    fn subscribe_wake(&self) -> watch::Receiver<u64> {
        watch::channel(0).1
    }
    fn subscribe_events(&self) -> ExecProcessEventReceiver {
        ExecProcessEventReceiver::empty()
    }
    fn read(
        &self,
        after: Option<u64>,
        max: Option<usize>,
        _: Option<u64>,
    ) -> ExecProcessFuture<'_, ReadResponse> {
        assert_eq!(after, Some(0));
        assert_eq!(max, None);
        Box::pin(async {
            Ok(ReadResponse {
                chunks: vec![ProcessOutputChunk {
                    seq: 1,
                    stream: ExecOutputStream::Stdout,
                    chunk: self.receipt.clone().into(),
                }],
                next_seq: 4,
                exited: true,
                exit_code: Some(0),
                closed: true,
                failure: None,
                sandbox_denied: false,
            })
        })
    }
    fn write(&self, chunk: Vec<u8>) -> ExecProcessFuture<'_, WriteResponse> {
        self.chunks.lock().unwrap().push(chunk);
        Box::pin(async {
            Ok(WriteResponse {
                status: if self.reject {
                    WriteStatus::StdinClosed
                } else {
                    WriteStatus::Accepted
                },
            })
        })
    }
    fn signal(&self, _: ProcessSignal) -> ExecProcessFuture<'_, ()> {
        panic!("unused")
    }
    fn terminate(&self) -> ExecProcessFuture<'_, ()> {
        self.terminated.store(true, Ordering::SeqCst);
        Box::pin(async { Ok(()) })
    }
}

#[tokio::test]
async fn exact_binary_chunks_and_empty_body_require_a_commit_receipt() {
    for size in [0, 1, CHUNK_BYTES * 2 + 17] {
        let bytes: Vec<_> = (0..size).map(|n| (n % 256) as u8).collect();
        let process = Process::new(
            format!("{{\"version\":1,\"outcome\":\"completed\",\"size_bytes\":{size}}}").as_bytes(),
        );
        assert_eq!(
            transfer(&process, &bytes).await.unwrap()["write"]["size_bytes"],
            size
        );
        let chunks = process.chunks.lock().unwrap();
        assert!(chunks.iter().all(|chunk| chunk.len() <= CHUNK_BYTES));
        assert_eq!(chunks[..chunks.len() - 1].concat(), bytes);
        assert_eq!(
            chunks.last().unwrap().as_slice(),
            Sha256::digest(&bytes).as_slice()
        );
        assert!(!process.terminated.load(Ordering::SeqCst));
    }
    let process = Process::new(b"");
    assert!(matches!(
        transfer(&process, b"data").await,
        Err(OperationError::Unsettled)
    ));
}

#[tokio::test]
async fn rejected_stdin_stops_without_replaying_or_reporting_commit() {
    let mut process = Process::new(b"");
    process.reject = true;
    assert!(matches!(
        transfer(&process, &vec![0; CHUNK_BYTES + 1]).await,
        Err(OperationError::Unsettled)
    ));
    assert_eq!(process.chunks.lock().unwrap().len(), 1);
    assert!(process.terminated.load(Ordering::SeqCst));
}

#[test]
fn only_complete_versioned_exact_receipts_resolve_the_write() {
    assert!(matches!(
        decode(
            br#"{"version":1,"outcome":"failed","error":"write_failed"}"#,
            3
        ),
        Err(OperationError::Rejected("native_error"))
    ));
    for value in [
        json!({"version":1,"outcome":"completed","size_bytes":2}),
        json!({"version":2,"outcome":"completed","size_bytes":3}),
        json!({"version":1,"outcome":"completed","size_bytes":3,"error":null}),
        json!({"version":1,"outcome":"unknown","error":"write_failed"}),
        json!({"version":1,"outcome":"failed","error":"unknown"}),
    ] {
        assert!(matches!(
            decode(&serde_json::to_vec(&value).unwrap(), 3),
            Err(OperationError::Unsettled)
        ));
    }
    for bytes in [
        b"{}".as_slice(),
        b"{",
        br#"{"version":1,"outcome":"completed","size_bytes":3}{}"#,
    ] {
        assert!(matches!(decode(bytes, 3), Err(OperationError::Unsettled)));
    }
}
