#[path = "../workspace_path.rs"]
mod workspace_path;
#[path = "../write_file.rs"]
mod write_file;

use serde_json::json;
use std::io::{self, Write};
use std::path::Path;

fn run() -> Result<u64, write_file::Failure> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.len() != 4 {
        return Err(io::Error::from(io::ErrorKind::InvalidInput).into());
    }
    let size = args[2]
        .parse::<u64>()
        .map_err(|_| io::Error::from(io::ErrorKind::InvalidInput))?;
    write_file::install(
        Path::new(&args[0]),
        &args[1],
        size,
        io::stdin().lock(),
        Path::new(&args[3]),
    )?;
    Ok(size)
}

fn main() -> std::process::ExitCode {
    let response = match run() {
        Ok(size) => json!({"version": 1, "outcome": "completed", "size_bytes": size}),
        Err(failure) => {
            json!({"version": 1, "outcome": if failure.committed { "unknown" } else { "failed" }, "error": if failure.error.kind() == io::ErrorKind::InvalidInput { "invalid_input" } else { "write_failed" }})
        }
    };
    if writeln!(io::stdout().lock(), "{response}").is_err() {
        return std::process::ExitCode::FAILURE;
    }
    std::process::ExitCode::SUCCESS
}
