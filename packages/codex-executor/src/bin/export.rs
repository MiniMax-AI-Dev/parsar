#[path = "../directory.rs"]
mod directory;
#[path = "../export.rs"]
mod export;
#[path = "../workspace_path.rs"]
mod workspace_path;

use std::{io, path::Path, process::ExitCode};

fn main() -> ExitCode {
    let args: Vec<_> = std::env::args_os().skip(1).collect();
    if args.len() != 1 {
        return ExitCode::FAILURE;
    }
    // A valid archive prefix alone is not success: callers must also observe exit 0.
    match export::outputs(Path::new(&args[0]), io::stdout().lock()) {
        Ok(()) => ExitCode::SUCCESS,
        Err(_) => ExitCode::FAILURE,
    }
}
