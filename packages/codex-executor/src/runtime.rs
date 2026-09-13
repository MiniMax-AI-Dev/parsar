use crate::options::Options;
use codex_exec_server::ExecServerRuntimePaths;
use std::io::Read;
use std::os::unix::fs::{DirBuilderExt, symlink};
use std::path::{Path, PathBuf};
use std::process::Command;

pub struct RuntimePaths {
    pub codex_home: PathBuf,
    pub native: ExecServerRuntimePaths,
}

pub fn prepare(options: &Options) -> Result<RuntimePaths, &'static str> {
    if !cfg!(all(target_os = "linux", target_arch = "x86_64")) {
        return Err("this executor currently supports Linux x86_64 only");
    }
    let base = match std::env::var_os("PARSAR_HOME") {
        Some(path) => PathBuf::from(path),
        None => PathBuf::from(std::env::var_os("HOME").ok_or("HOME is required")?).join(".parsar"),
    };
    if !base.is_absolute() {
        return Err("executor state directory must be absolute");
    }
    let state = base.join("codex-executor").join(&options.environment_id);
    let codex_home = state.join("codex");
    private_directory(&codex_home)?;
    let binary = options
        .codex_bin
        .canonicalize()
        .map_err(|_| "native Codex executable is unavailable")?;
    let mut magic = [0u8; 4];
    std::fs::File::open(&binary)
        .and_then(|mut file| file.read_exact(&mut magic))
        .map_err(|_| "could not read native Codex executable")?;
    if magic != *b"\x7fELF" {
        return Err("--codex-bin requires the native ELF executable, not a wrapper");
    }
    let version = Command::new(&binary)
        .arg("--version")
        .env("CODEX_HOME", &codex_home)
        .output()
        .map_err(|_| "could not verify native Codex executable")?;
    if !version.status.success()
        || String::from_utf8_lossy(&version.stdout).trim() != "codex-cli 0.153.4"
    {
        return Err("native Codex 0.153.4 is required");
    }
    let sandbox = state.join("codex-linux-sandbox");
    match std::fs::read_link(&sandbox) {
        Ok(target) if target == binary => {}
        Ok(_) => return Err("executor sandbox helper points to another installation"),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            symlink(&binary, &sandbox).map_err(|_| "could not create executor sandbox helper")?;
        }
        Err(_) => return Err("executor sandbox helper is unavailable"),
    }
    let native = ExecServerRuntimePaths::new(binary, Some(sandbox))
        .map_err(|_| "invalid native helper paths")?;
    Ok(RuntimePaths { codex_home, native })
}

fn private_directory(path: &Path) -> Result<(), &'static str> {
    std::fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(path)
        .map_err(|_| "could not create private executor state")
}
