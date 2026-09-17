use super::super::super::process_output::Output;
use super::*;
use codex_exec_server::{ExecOutputStream, ProcessOutputChunk, ReadResponse};

fn response(next_seq: u64, chunks: &[(u64, &[u8])], exited: bool, closed: bool) -> ReadResponse {
    ReadResponse {
        chunks: chunks
            .iter()
            .map(|(seq, bytes)| ProcessOutputChunk {
                seq: *seq,
                stream: ExecOutputStream::Stdout,
                chunk: bytes.to_vec().into(),
            })
            .collect(),
        next_seq,
        exited,
        exit_code: exited.then_some(0),
        closed,
        failure: None,
        sandbox_denied: false,
    }
}

#[test]
fn complete_snapshots_account_for_exit_before_late_output() {
    let mut output = Output::default();
    output
        .append(&response(2, &[(1, b"first")], false, false))
        .expect("first snapshot");
    // Exit consumes sequence 2; output can still arrive as sequence 3.
    output
        .append(&response(4, &[(3, b"last")], true, false))
        .expect("late output");
    output
        .append(&response(5, &[], true, true))
        .expect("closed");
    assert_eq!(output.bytes, b"firstlast");
    assert_eq!(output.after(), 4);
}

#[test]
fn missing_retained_output_is_rejected_even_if_tail_is_valid_json() {
    let valid = br#"{"version":1,"directory":{"entries":[],"truncated":false}}"#;
    assert!(
        Output::default()
            .append(&response(6, &[(3, valid)], true, true))
            .is_err()
    );
    // Closed on a capped snapshot cannot hide missing output chunks.
    assert!(
        Output::default()
            .append(&response(5, &[(1, valid)], true, true))
            .is_err()
    );
    assert!(
        Output::default()
            .append(&response(5, &[(1, b"a"), (1, b"b")], true, true))
            .is_err()
    );
}

#[test]
fn output_is_bounded_and_rejects_mixed_streams_and_regression() {
    let mut output = Output::default();
    let bytes = vec![b'x'; 1024 * 1024];
    for sequence in 1..=4 {
        output
            .append(&response(sequence + 1, &[(sequence, &bytes)], false, false))
            .expect("bounded");
    }
    assert!(
        output
            .append(&response(6, &[(5, b"x")], false, false))
            .is_err()
    );
    let mut mixed = response(4, &[(1, b"x")], true, true);
    mixed.chunks[0].stream = ExecOutputStream::Stderr;
    assert!(Output::default().append(&mixed).is_err());
    let mut output = Output::default();
    output.append(&response(2, &[], true, false)).expect("exit");
    assert!(output.append(&response(2, &[], false, false)).is_err());
}

#[test]
fn only_versioned_complete_bounded_directory_envelopes_are_accepted() {
    let valid = br#"{"version":1,"directory":{"entries":[{"name":"a","kind":"file","size_bytes":0}],"truncated":false}}"#;
    assert!(decode(valid, 1).is_ok());
    assert!(decode(valid, 0).is_err());
    for bytes in [
        br#"{"version":2,"directory":{"entries":[],"truncated":false}}"#.as_slice(),
        br#"{"version":1,"directory":{"entries":[]}}"#,
        br#"{"version":1,"directory":{"entries":[],"truncated":false},"error":"not_found"}"#,
        br#"{"version":1,"directory":{"entries":[{"name":"../a","kind":"file","size_bytes":0}],"truncated":false}}"#,
        br#"{"version":1,"directory":{"entries":[{"name":"a","kind":"symlink","size_bytes":1}],"truncated":false}}"#,
        br#"{"version":1,"directory":{"entries":[],"truncated":false}}{}"#,
    ] { assert!(decode(bytes, 1).is_err(), "{bytes:?}"); }
}

#[test]
fn helper_selector_cannot_be_relative_or_inside_the_workspace() {
    use std::path::Path;
    for helper in [
        "relative",
        "/workspace/helper",
        "/workspace/nested/helper",
        "/trusted/../helper",
        "/trusted//helper",
        "/trusted/helper/",
    ] {
        assert!(!super::super::qualified_path(
            Path::new(helper),
            Path::new("/workspace")
        ));
    }
    assert!(super::super::qualified_path(
        Path::new("/usr/local/bin/helper"),
        Path::new("/workspace")
    ));
}
