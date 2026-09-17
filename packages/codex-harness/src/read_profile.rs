use anyhow::{Result, ensure};
use codex_config::LoaderOverrides;
use std::path::Path;

// Read preparation has no execution configuration. Keep native security
// requirements, while excluding host/user/project model, MCP and plugin settings.
pub fn loader(home: &Path) -> Result<LoaderOverrides> {
    loader_with_legacy_path(home, Path::new("/etc/codex/managed_config.toml"))
}

fn loader_with_legacy_path(home: &Path, legacy: &Path) -> Result<LoaderOverrides> {
    // Native legacy config also supplies enforced requirements. Do not silently
    // remove those constraints while isolating execution configuration.
    ensure!(
        !legacy.try_exists()?,
        "read preparation requires separate native requirements, not legacy managed config"
    );
    ensure!(
        home.is_absolute(),
        "read preparation requires an absolute home"
    );
    let empty = home.join("read-config.toml");
    std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&empty)?;
    Ok(LoaderOverrides {
        system_config_path: Some(empty.clone()),
        managed_config_path: Some(empty),
        ignore_user_config: true,
        ignore_project_config: true,
        ..LoaderOverrides::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use codex_core::config::{ConfigBuilder, ConfigOverrides};

    #[test]
    fn rejects_legacy_requirements_instead_of_dropping_them() -> Result<()> {
        let state = std::path::PathBuf::from(std::env::var_os("HOME").unwrap()).join(".parsar");
        let root = tempfile::Builder::new()
            .prefix("read-legacy-")
            .tempdir_in(state)?;
        let legacy = root.path().join("managed_config.toml");
        std::fs::write(&legacy, "approval_policy = 'never'\n")?;
        assert!(loader_with_legacy_path(root.path(), &legacy).is_err());
        assert!(!root.path().join("read-config.toml").exists());
        Ok(())
    }

    #[tokio::test]
    async fn ignores_execution_layers_but_keeps_security_requirements() -> Result<()> {
        let state = std::path::PathBuf::from(std::env::var_os("HOME").unwrap()).join(".parsar");
        let root = tempfile::Builder::new()
            .prefix("read-config-")
            .tempdir_in(state)?;
        let home = root.path().join("home");
        let project = root.path().join("project");
        std::fs::create_dir_all(&home)?;
        std::fs::create_dir_all(project.join(".codex"))?;
        let sentinel =
            "model = 'must-not-load'\n[mcp_servers.sentinel]\ncommand = '/must-not-run'\n";
        std::fs::write(home.join("config.toml"), sentinel)?;
        std::fs::write(project.join(".codex/config.toml"), sentinel)?;
        let overrides = loader(&home)?;
        assert_eq!(overrides.system_config_path, overrides.managed_config_path);
        assert!(std::fs::read(overrides.system_config_path.as_ref().unwrap())?.is_empty());
        assert!(!overrides.ignore_managed_requirements);
        assert!(!overrides.ignore_login_requirements);
        assert!(overrides.system_requirements_path.is_none());
        let config = ConfigBuilder::default()
            .codex_home(home)
            .loader_overrides(overrides)
            .harness_overrides(ConfigOverrides {
                cwd: Some(project),
                ..Default::default()
            })
            .build()
            .await?;
        assert_ne!(config.model.as_deref(), Some("must-not-load"));
        assert!(config.mcp_servers.get().is_empty());
        Ok(())
    }
}
