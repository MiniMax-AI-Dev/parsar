use super::*;

const ENVIRONMENT: &str = "20cc9e86-39a0-42ca-a2cd-218c7cd2eced";
const OTHER_ENVIRONMENT: &str = "1eb47f85-842d-43f2-bbca-42b54cf127cc";
const KEY_ID: &str = "b626f2e2-4678-434c-a40b-f7be962bc4ea";
const TOKEN: &str = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG";

fn credential() -> serde_json::Value {
    serde_json::json!({"key_id": KEY_ID, "executor_token": TOKEN})
}

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
fn principal_credential_accepts_omitted_or_null_environment_without_exposing_secret_in_debug() {
    let unrestricted = credential();
    let mut null_restriction = credential();
    null_restriction["environment_id"] = serde_json::Value::Null;
    for json in [unrestricted, null_restriction] {
        for environment in [ENVIRONMENT, OTHER_ENVIRONMENT] {
            let header = credential_header(&json.to_string(), environment).unwrap();
            assert_eq!(header.to_str().unwrap(), format!("Bearer {TOKEN}"));
            assert!(header.is_sensitive());
            assert!(!format!("{header:?}").contains(TOKEN));
        }
    }
}

#[test]
fn exact_credential_requires_its_canonical_environment() {
    let mut json = credential();
    json["environment_id"] = ENVIRONMENT.into();
    let json = json.to_string();
    let header = credential_header(&json, ENVIRONMENT).unwrap();
    assert_eq!(header.to_str().unwrap(), format!("Bearer {TOKEN}"));
    assert!(header.is_sensitive());
    assert!(!format!("{header:?}").contains(TOKEN));
    let error = credential_header(&json, OTHER_ENVIRONMENT).unwrap_err();
    assert_eq!(
        error,
        "executor credential belongs to a different Environment"
    );
    assert!(!error.contains(TOKEN));
    for environment in ["invalid".to_owned(), ENVIRONMENT.to_uppercase()] {
        let mut json = credential();
        json["environment_id"] = environment.clone().into();
        let error = credential_header(&json.to_string(), &environment).unwrap_err();
        assert!(!error.contains(TOKEN));
    }
}

#[test]
fn old_credential_without_key_id_fails_clearly() {
    let json = serde_json::json!({"environment_id": ENVIRONMENT, "executor_token": TOKEN});
    let error = credential_header(&json.to_string(), ENVIRONMENT).unwrap_err();
    assert!(error.contains("key_id"));
    assert!(!error.contains(TOKEN));
}

#[test]
fn key_id_requires_a_canonical_nonzero_uuid_without_exposing_invalid_values() {
    for key_id in [
        serde_json::Value::Null,
        serde_json::json!(123),
        "".into(),
        "00000000-0000-0000-0000-000000000000".into(),
        KEY_ID.to_uppercase().into(),
        KEY_ID.replace('-', "").into(),
        TOKEN.into(),
    ] {
        let mut json = credential();
        json["key_id"] = key_id;
        let error = credential_header(&json.to_string(), ENVIRONMENT).unwrap_err();
        assert!(!error.contains(TOKEN));
    }
}

#[test]
fn malformed_json_unknown_fields_and_invalid_tokens_do_not_expose_secrets() {
    let mut unknown_field = credential();
    unknown_field[TOKEN] = TOKEN.into();
    let json = credential().to_string();
    for invalid in [
        format!("{{ not json {TOKEN}"),
        unknown_field.to_string(),
        json.replace(TOKEN, "sk-not-an-issued-executor-key"),
        json.replace(TOKEN, &"a".repeat(42)),
        json.replace(TOKEN, &"a".repeat(44)),
        json.replace(TOKEN, &format!("{}+", "a".repeat(42))),
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
    assert!(opts.authorization().await.is_err());
    let json = credential().to_string();
    std::fs::write(&opts.credentials, &json).unwrap();
    std::fs::set_permissions(&opts.credentials, std::fs::Permissions::from_mode(0o644)).unwrap();
    assert!(opts.authorization().await.is_err());
    std::fs::set_permissions(&opts.credentials, std::fs::Permissions::from_mode(0o600)).unwrap();
    assert!(opts.authorization().await.is_ok());
    let link = base.join("alias.json");
    symlink(&opts.credentials, &link).unwrap();
    opts.credentials = link;
    assert!(opts.authorization().await.is_err());
    opts.credentials = base.clone();
    assert!(opts.authorization().await.is_err());
    opts.credentials = base.join("credential.json");
    let mut padded_json = json;
    padded_json.push_str(&" ".repeat(65536 - padded_json.len()));
    std::fs::write(&opts.credentials, &padded_json).unwrap();
    assert!(opts.authorization().await.is_ok());
    padded_json.push(' ');
    std::fs::write(&opts.credentials, &padded_json).unwrap();
    let error = opts.authorization().await.unwrap_err();
    assert!(!error.contains(TOKEN));
    assert!(error.contains("64 KiB"));
    std::fs::remove_dir_all(base).unwrap();
}
