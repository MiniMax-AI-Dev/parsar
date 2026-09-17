use super::OperationError;
use codex_exec_server::{ExecOutputStream, ReadResponse};
use serde::Deserialize;
use serde_json::{Value, json};

const MAX_OUTPUT: usize = 4 * 1024 * 1024;

#[derive(Default)]
pub(super) struct Output {
    pub bytes: Vec<u8>,
    cursor: u64,
    exited: bool,
    closed: bool,
}

impl Output {
    pub fn after(&self) -> u64 {
        self.cursor
    }

    pub fn append(&mut self, response: &ReadResponse) -> Result<(), ()> {
        let next = response.next_seq.checked_sub(1).ok_or(())?;
        if next < self.cursor
            || (self.exited && !response.exited)
            || (self.closed && !response.closed)
            || response.exited != response.exit_code.is_some()
            || (response.closed && !response.exited)
        {
            return Err(());
        }
        let events = response.chunks.len() as u64
            + u64::from(response.exited && !self.exited)
            + u64::from(response.closed && !self.closed);
        if next - self.cursor != events {
            return Err(());
        }
        let mut previous = self.cursor;
        for chunk in &response.chunks {
            if chunk.seq <= previous
                || chunk.seq > next
                || chunk.stream != ExecOutputStream::Stdout
                || chunk.chunk.0.len() > MAX_OUTPUT.saturating_sub(self.bytes.len())
            {
                return Err(());
            }
            previous = chunk.seq;
            self.bytes.extend_from_slice(&chunk.chunk.0);
        }
        self.cursor = next;
        self.exited = response.exited;
        self.closed = response.closed;
        Ok(())
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Envelope {
    version: u32,
    directory: Option<Directory>,
    error: Option<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Directory {
    entries: Vec<Entry>,
    truncated: bool,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Entry {
    name: String,
    kind: String,
    size_bytes: Option<i64>,
}

pub(super) fn decode(bytes: &[u8], limit: usize) -> Result<Value, OperationError> {
    let invalid = || OperationError::Rejected("native_error");
    let envelope: Envelope = serde_json::from_slice(bytes).map_err(|_| invalid())?;
    if envelope.version != 1 {
        return Err(invalid());
    }
    match (envelope.directory, envelope.error) {
        (None, Some(error)) => Err(OperationError::Rejected(match error.as_str() {
            "not_found" => "not_found",
            "permission_denied" => "permission_denied",
            "invalid_path" => "invalid_path",
            "too_large" => "too_large",
            _ => "native_error",
        })),
        (Some(directory), None) if directory.entries.len() <= limit => {
            let mut seen = std::collections::HashSet::new();
            let mut entries = Vec::with_capacity(directory.entries.len());
            for entry in directory.entries {
                if entry.name.is_empty()
                    || entry.name.len() > 255
                    || entry.name == "."
                    || entry.name == ".."
                    || entry.name.contains(['/', '\\', '\0', '\r', '\n'])
                    || !seen.insert(entry.name.clone())
                {
                    return Err(invalid());
                }
                match (entry.kind.as_str(), entry.size_bytes) {
                    ("file", Some(size)) if size >= 0 => {}
                    ("directory" | "symlink" | "other", None) => {}
                    _ => return Err(invalid()),
                }
                entries.push(
                    json!({"name":entry.name,"kind":entry.kind,"size_bytes":entry.size_bytes}),
                );
            }
            Ok(json!({"directory":{"entries":entries,"truncated":directory.truncated}}))
        }
        _ => Err(invalid()),
    }
}

#[cfg(test)]
#[path = "files_directory_output_tests.rs"]
mod tests;
