use anyhow::{Context, Result, ensure};
use clap::{Parser, Subcommand};
use codex_features::is_known_feature_key;
use codex_utils_cli::CliConfigOverrides;
use std::path::{Component, Path, PathBuf};
use std::time::Duration;
use uuid::Uuid;

#[derive(Parser)]
#[command(version, about = "Private exact-pin Parsar Codex harness integration")]
pub struct Cli {
    #[arg(long, global = true)]
    pub workspace_read_only: bool,
    #[command(flatten)]
    config: CliConfigOverrides,
    #[arg(long, global = true)]
    enable: Vec<String>,
    #[arg(long, global = true)]
    disable: Vec<String>,
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    AppServer {
        #[arg(long, required = true)]
        stdio: bool,
    },
}

impl Cli {
    pub fn overrides(self) -> Result<CliConfigOverrides> {
        let mut config = self.config;
        for (features, enabled) in [(self.enable, true), (self.disable, false)] {
            for feature in features {
                ensure!(is_known_feature_key(&feature), "unknown native feature");
                config
                    .raw_overrides
                    .push(format!("features.{feature}={enabled}"));
            }
        }
        Ok(config)
    }
}

#[path = "options_write.rs"]
mod write;
pub use write::WriteBinding;

pub struct Binding {
    pub write: Option<WriteBinding>,
    pub native_binary: PathBuf,
    pub directory_helper: Option<PathBuf>,
    pub environment: String,
    pub workspace: PathBuf,
    pub ipc_root: PathBuf,
}

impl Binding {
    pub fn from_environment() -> Result<Self> {
        fn required(suffix: &str) -> Result<String> {
            std::env::var(format!("PARSAR_CODEX_HARNESS_{suffix}"))
                .context("explicit private harness configuration is required")
        }
        let workspace = PathBuf::from(required("WORKSPACE")?);
        let binding = Self {
            write: WriteBinding::from_environment(&workspace)?,
            native_binary: PathBuf::from(required("NATIVE")?),
            directory_helper: std::env::var_os("PARSAR_CODEX_HARNESS_DIRECTORY_HELPER")
                .filter(|value| !value.is_empty())
                .map(PathBuf::from),
            environment: required("ENVIRONMENT")?,
            workspace,
            ipc_root: PathBuf::from(required("IPC_ROOT")?),
        };
        let id = Uuid::parse_str(&binding.environment).context("invalid Environment identity")?;
        ensure!(
            !id.is_nil() && id.to_string() == binding.environment,
            "canonical Environment UUID required"
        );
        ensure!(
            std::env::var("CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID")
                .ok()
                .as_deref()
                == Some(binding.environment.as_str()),
            "native registry Environment must match the operator binding"
        );
        ensure!(
            clean_absolute(&binding.native_binary),
            "native helper requires an absolute path"
        );
        ensure!(
            clean_absolute(&binding.workspace),
            "remote workspace requires an absolute path"
        );
        ensure!(
            clean_absolute(&binding.ipc_root),
            "IPC root requires an absolute path"
        );
        Ok(binding)
    }

    pub fn restrict_reads(&mut self, read_only: bool) {
        if read_only {
            self.write = None;
        }
    }

    pub async fn check_native(&self) -> Result<()> {
        let mut command = tokio::process::Command::new(&self.native_binary);
        command.arg("--version").kill_on_drop(true);
        let output = tokio::time::timeout(Duration::from_secs(5), command.output())
            .await
            .context("native version probe timed out")?
            .context("native version probe failed")?;
        ensure!(
            output.status.success() && output.stdout == b"codex-cli 0.153.4\n",
            "matching stock Codex 0.153.4 helper required"
        );
        Ok(())
    }
}

fn clean_absolute(path: &Path) -> bool {
    path.is_absolute()
        && path
            .components()
            .all(|part| matches!(part, Component::RootDir | Component::Normal(_)))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_existing_rpc_arguments_and_rejects_unknown_modes() -> Result<()> {
        let args = Cli::try_parse_from([
            "harness",
            "-c",
            "model=example",
            "app-server",
            "--stdio",
            "--disable",
            "multi_agent",
        ])?;
        let config = args.overrides()?;
        assert_eq!(
            config.raw_overrides,
            ["model=example", "features.multi_agent=false"]
        );
        assert!(
            Cli::try_parse_from(["harness", "app-server", "--listen", "ws://0.0.0.0:1"]).is_err()
        );
        assert!(Cli::try_parse_from(["harness", "exec"]).is_err());
        Ok(())
    }
}
