use std::path::{Path, PathBuf};

use anyhow::{Context, Result, ensure};

pub fn overrides() -> Vec<(String, toml::Value)> {
    let mut overrides: Vec<(String, toml::Value)> = vec![
        ("model", "MiniMax-M3"),
        ("model_provider", "shared_files"),
        ("approval_policy", "never"),
        ("sandbox_mode", "danger-full-access"),
        ("web_search", "disabled"),
        ("shell_environment_policy.inherit", "core"),
        (
            "model_providers.shared_files.name",
            "Shared native files acceptance",
        ),
        (
            "model_providers.shared_files.base_url",
            "https://api.minimax.cn/v1",
        ),
        (
            "model_providers.shared_files.env_key",
            "PARSAR_PROBE_MODEL_KEY",
        ),
        ("model_providers.shared_files.wire_api", "responses"),
    ]
    .into_iter()
    .map(|(key, value)| (key.to_owned(), toml::Value::String(value.to_owned())))
    .collect();
    overrides.push((
        "features.multi_agent".to_owned(),
        toml::Value::Boolean(false),
    ));
    overrides.push((
        "shell_environment_policy.ignore_default_excludes".to_owned(),
        toml::Value::Boolean(false),
    ));
    overrides.push((
        "shell_environment_policy.exclude".to_owned(),
        toml::Value::Array(vec![
            toml::Value::String("PARSAR_PLACEMENT_MODEL_KEY_FILE".to_owned()),
            toml::Value::String("PARSAR_PROBE_MODEL_KEY".to_owned()),
            toml::Value::String("CODEX_EXEC_SERVER_NOISE_*".to_owned()),
        ]),
    ));
    overrides
}

pub fn caller_state_root() -> Result<PathBuf> {
    let home = PathBuf::from(
        std::env::var_os("PARSAR_SHARED_FILES_CALLER_HOME")
            .context("original caller HOME is required")?,
    );
    ensure!(home.is_absolute(), "original caller HOME must be absolute");
    std::fs::canonicalize(home.join(".parsar")).context("canonical caller state root is missing")
}

pub fn proof_root(root: &Path, state_root: &Path) -> Result<PathBuf> {
    ensure!(root.is_absolute(), "proof root must be absolute");
    let root = std::fs::canonicalize(root).context("canonical proof root is missing")?;
    ensure!(
        root.is_dir() && root.starts_with(state_root),
        "proof root must be under caller ~/.parsar"
    );
    Ok(root)
}

#[cfg(test)]
mod tests {
    use super::proof_root;
    use std::path::PathBuf;

    #[test]
    fn proof_root_uses_canonical_caller_boundary() -> anyhow::Result<()> {
        // Even rejected test paths stay below the real caller's private state root.
        let private =
            PathBuf::from(std::env::var_os("HOME").ok_or_else(|| anyhow::anyhow!("HOME missing"))?)
                .join(".parsar")
                .canonicalize()?;
        let fixture = tempfile::Builder::new()
            .prefix("files-root-test-")
            .tempdir_in(private)?;
        let state = fixture.path().join("caller/.parsar");
        let accepted = state.join("proof");
        let outside = fixture.path().join("outside");
        let impostor = outside.join(".parsar/proof");
        std::fs::create_dir_all(&accepted)?;
        std::fs::create_dir_all(&impostor)?;
        let state = state.canonicalize()?;
        assert_eq!(proof_root(&accepted, &state)?, accepted.canonicalize()?);
        assert!(proof_root(&impostor, &state).is_err());
        assert!(proof_root(&state.join("../../outside"), &state).is_err());
        #[cfg(unix)]
        {
            std::os::unix::fs::symlink(&outside, state.join("escape"))?;
            assert!(proof_root(&state.join("escape"), &state).is_err());
        }
        Ok(())
    }
}
