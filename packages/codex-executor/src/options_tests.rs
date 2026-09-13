use super::*;

const ENVIRONMENT: &str = "20cc9e86-39a0-42ca-a2cd-218c7cd2eced";
const TOKEN: &str = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG";

fn options() -> Options {
    Options {
        remote: "https://registry.example.test/prefix".into(),
        environment_id: ENVIRONMENT.into(),
        credentials: "/private/executor.json".into(),
        codex_bin: "/opt/codex/bin/codex".into(),
    }
}

#[test]
fn explicit_third_party_https_and_loopback_are_supported() {
    for remote in [
        "https://registry.example.test/prefix",
        "https://10.0.0.10:8443",
        "http://127.0.0.1:8000",
        "http://[::1]:8000",
    ] {
        assert!(validate_remote(remote).is_ok());
    }
    for remote in [
        "http://registry.example.test",
        "http://10.0.0.10",
        "https://user:secret@registry.example.test",
        "https://registry.example.test?token=secret",
        "https://registry.example.test#secret",
        "file:///private/config",
    ] {
        assert!(validate_remote(remote).is_err());
    }
}

#[test]
fn ambiguous_environment_and_relative_paths_are_rejected() {
    let mut opts = options();
    opts.environment_id = ENVIRONMENT.to_uppercase();
    assert!(opts.validate().is_err());
    opts = options();
    opts.credentials = "executor.json".into();
    assert!(opts.validate().is_err());
}

#[test]
fn credential_is_exact_environment_and_does_not_expose_secret_in_errors_or_debug() {
    let json = serde_json::json!({
        "environment_id": ENVIRONMENT,
        "executor_token": TOKEN,
    })
    .to_string();
    let header = credential_header(&json, ENVIRONMENT).unwrap();
    assert_eq!(header.to_str().unwrap(), format!("Bearer {TOKEN}"));
    assert!(!format!("{header:?}").contains(TOKEN));
    assert!(credential_header(&json, "another-environment").is_err());
    for invalid in [
        "{ not json".to_owned(),
        json.replace(TOKEN, "sk-not-an-issued-executor-key"),
        json.replace("executor_token", "api_key"),
    ] {
        let error = credential_header(&invalid, ENVIRONMENT).unwrap_err();
        assert!(!error.contains(TOKEN));
    }
}

#[tokio::test]
async fn only_private_regular_credential_files_are_accepted() {
    use std::os::unix::fs::{PermissionsExt, symlink};

    let base = PathBuf::from(std::env::var_os("HOME").unwrap())
        .join(".parsar")
        .join("executor-credential-tests")
        .join(Uuid::new_v4().to_string());
    std::fs::create_dir_all(&base).unwrap();
    let mut opts = options();
    opts.credentials = base.join("credential.json");
    std::fs::write(
        &opts.credentials,
        serde_json::json!({"environment_id": ENVIRONMENT, "executor_token": TOKEN}).to_string(),
    )
    .unwrap();
    std::fs::set_permissions(&opts.credentials, std::fs::Permissions::from_mode(0o644)).unwrap();
    assert!(opts.authorization().await.is_err());
    std::fs::set_permissions(&opts.credentials, std::fs::Permissions::from_mode(0o600)).unwrap();
    assert!(opts.authorization().await.is_ok());
    let link = base.join("alias.json");
    symlink(&opts.credentials, &link).unwrap();
    opts.credentials = link;
    assert!(opts.authorization().await.is_err());
    std::fs::remove_dir_all(base).unwrap();
}
