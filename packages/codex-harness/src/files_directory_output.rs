use super::OperationError;
use serde::Deserialize;
use serde_json::{Value, json};

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
