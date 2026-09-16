#[path = "shared_files/cancellation.rs"]
mod cancellation;
#[path = "shared_files/configuration.rs"]
mod configuration;
#[path = "shared_files/files.rs"]
mod files;
#[path = "shared_files/observations.rs"]
mod observations;
#[path = "shared_files/probe.rs"]
mod probe;
#[path = "shared_files/raw_runtime.rs"]
mod runtime;

fn main() -> std::process::ExitCode {
    probe::main(true)
}
