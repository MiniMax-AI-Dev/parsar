use anyhow::{Context, Result, bail, ensure};
use std::path::{Path, PathBuf};

#[derive(Clone)]
pub struct WriteBinding {
    pub helper: PathBuf,
    pub staging: PathBuf,
    pub parent: PathBuf,
}

impl WriteBinding {
    pub fn from_environment(workspace: &Path) -> Result<Option<Self>> {
        let helper = std::env::var_os("PARSAR_CODEX_HARNESS_WRITE_HELPER").map(PathBuf::from);
        let staging = std::env::var_os("PARSAR_CODEX_HARNESS_STAGING").map(PathBuf::from);
        Self::from_paths(workspace, helper, staging)
    }

    pub fn from_paths(
        workspace: &Path,
        helper: Option<PathBuf>,
        staging: Option<PathBuf>,
    ) -> Result<Option<Self>> {
        let (helper, staging) = match (helper, staging) {
            (None, None) => return Ok(None),
            (Some(helper), Some(staging)) => (helper, staging),
            _ => bail!("write helper and protected staging must be configured together"),
        };
        ensure!(
            clean(workspace) && clean(&helper) && clean(&staging),
            "write binding requires clean absolute paths"
        );
        let parent = workspace
            .parent()
            .filter(|parent| *parent != Path::new("/"))
            .context("workspace must have a non-root Environment parent")?;
        ensure!(
            staging.parent() == Some(parent) && staging != workspace,
            "workspace and staging must be distinct siblings below one private Environment parent"
        );
        let parent = parent.to_owned();
        ensure!(
            !helper.starts_with(&parent),
            "write helper must be outside the writable Environment parent"
        );
        Ok(Some(Self {
            helper,
            staging,
            parent,
        }))
    }
}

fn clean(path: &Path) -> bool {
    path.to_str().is_some_and(|value| {
        value.starts_with('/')
            && !value.contains(['\\', '\0', '\r', '\n'])
            && value
                .split('/')
                .skip(1)
                .all(|part| !part.is_empty() && part != "." && part != "..")
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_explicit_disjoint_siblings_with_external_helper_are_admitted() {
        let workspace = Path::new("/data/workspace");
        assert!(
            WriteBinding::from_paths(workspace, None, None)
                .unwrap()
                .is_none()
        );
        for (helper, staging) in [
            (Some("/bin/helper"), None),
            (None, Some("/data/staging")),
            (Some("/data/helper"), Some("/data/staging")),
            (Some("/bin/helper"), Some("/data/workspace")),
            (Some("/bin/helper"), Some("/data/workspace/staging")),
            (Some("/bin/helper"), Some("/other/staging")),
            (Some("/bin//helper"), Some("/data/staging")),
            (Some("/bin/helper"), Some("/data/../staging")),
        ] {
            assert!(
                WriteBinding::from_paths(
                    workspace,
                    helper.map(Into::into),
                    staging.map(Into::into)
                )
                .is_err()
            );
        }
        assert!(
            WriteBinding::from_paths(
                Path::new("/workspace"),
                Some("/bin/helper".into()),
                Some("/staging".into())
            )
            .is_err()
        );
        assert!(
            WriteBinding::from_paths(
                workspace,
                Some("/bin/helper".into()),
                Some("/data/staging".into())
            )
            .unwrap()
            .is_some()
        );
    }
}
