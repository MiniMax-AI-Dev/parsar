use rustix::fs::FileType;
#[path = "../directory.rs"]
mod directory;
use directory::observe;
#[path = "../workspace_path.rs"]
mod workspace_path;
use serde_json::json;
use std::{io, path::Path};
use workspace_path::{anchor, directory};

fn run() -> io::Result<serde_json::Value> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.len() != 3 {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    let limit = args[2]
        .parse()
        .map_err(|_| io::Error::from(io::ErrorKind::InvalidInput))?;
    if !(1..=4096).contains(&limit) {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    let root = anchor(Path::new(&args[0]))?;
    let selected = directory(&root, &args[1])?;
    let result = observe(&selected, limit)?;
    let entries: Vec<_> = result
        .entries
        .into_iter()
        .map(|entry| {
            let kind = match entry.kind {
                FileType::RegularFile => "file",
                FileType::Directory => "directory",
                FileType::Symlink => "symlink",
                _ => "other",
            };
            json!({"name": entry.name, "kind": kind, "size_bytes": entry.size})
        })
        .collect();
    Ok(json!({"version": 1, "directory": {"entries": entries, "truncated": result.truncated}}))
}

fn main() -> std::process::ExitCode {
    let response = match run() {
        Ok(value) => value,
        Err(error) => json!({"version": 1, "error": match error.kind() {
            io::ErrorKind::NotFound => "not_found",
            io::ErrorKind::PermissionDenied => "permission_denied",
            io::ErrorKind::InvalidInput | io::ErrorKind::NotADirectory => "invalid_path",
            _ => "native_error",
        }}),
    };
    let bytes = match serde_json::to_vec(&response) {
        Ok(bytes) if bytes.len() <= 4 * 1024 * 1024 => bytes,
        _ => b"{\"version\":1,\"error\":\"too_large\"}".to_vec(),
    };
    use io::Write;
    match io::stdout().lock().write_all(&bytes) {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(_) => std::process::ExitCode::FAILURE,
    }
}
