use clap::Parser;
use codex_exec_server::read_sensitive_file_to_string;
use http::HeaderValue;
use serde::Deserialize;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use url::{Host, Url};
use uuid::Uuid;

#[derive(Parser)]
#[command(
    version,
    about = "Agents API executor using Codex 0.153.4 native libraries. Requires the matching native Codex installation."
)]
pub struct Options {
    /// Third-party registry HTTPS URL; HTTP is allowed only on loopback for development.
    #[arg(long)]
    pub remote: String,
    /// Canonical Environment UUID issued by Agents API.
    #[arg(long)]
    pub environment_id: String,
    /// Absolute path to mode-0600 JSON from agents-api-environment-key.
    #[arg(long)]
    pub credentials: PathBuf,
    /// Absolute path to the native Codex 0.153.4 executable, with its installation resources intact.
    #[arg(long)]
    pub codex_bin: PathBuf,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Credential {
    environment_id: String,
    executor_token: String,
}

impl Options {
    pub fn validate(&self) -> Result<(), &'static str> {
        validate_remote(&self.remote)?;
        let id = Uuid::parse_str(&self.environment_id)
            .map_err(|_| "environment ID must be a canonical UUID")?;
        if id.to_string() != self.environment_id {
            return Err("environment ID must be a canonical UUID");
        }
        if !self.credentials.is_absolute() || !self.codex_bin.is_absolute() {
            return Err("credentials and native Codex paths must be absolute");
        }
        Ok(())
    }

    pub async fn authorization(&self) -> Result<HeaderValue, &'static str> {
        let metadata = tokio::fs::symlink_metadata(&self.credentials)
            .await
            .map_err(|_| "could not read executor credential file")?;
        if !metadata.is_file()
            || metadata.len() > 65536
            || metadata.permissions().mode() & 0o077 != 0
        {
            return Err(
                "executor credentials require a private regular file (mode 0600, at most 64 KiB)",
            );
        }
        let json = read_sensitive_file_to_string(&self.credentials)
            .await
            .map_err(|_| "could not read executor credential file")?;
        credential_header(&json, &self.environment_id)
    }
}

fn validate_remote(remote: &str) -> Result<(), &'static str> {
    let url = Url::parse(remote).map_err(|_| "invalid executor registry URL")?;
    let loopback = match url.host() {
        Some(Host::Domain(host)) => host.eq_ignore_ascii_case("localhost"),
        Some(Host::Ipv4(ip)) => ip.is_loopback(),
        Some(Host::Ipv6(ip)) => ip.is_loopback(),
        None => false,
    };
    if url.host().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || !(url.scheme() == "https" || (url.scheme() == "http" && loopback))
    {
        return Err(
            "registry URL requires HTTPS (HTTP only on loopback), without userinfo, query or fragment",
        );
    }
    Ok(())
}

fn credential_header(json: &str, environment: &str) -> Result<HeaderValue, &'static str> {
    let credential: Credential =
        serde_json::from_str(json).map_err(|_| "invalid executor credential JSON")?;
    if credential.environment_id != environment {
        return Err("executor credential belongs to a different Environment");
    }
    if credential.executor_token.len() != 43
        || !credential
            .executor_token
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
    {
        return Err("invalid issued executor credential");
    }
    let mut header = HeaderValue::from_str(&format!("Bearer {}", credential.executor_token))
        .map_err(|_| "invalid issued executor credential")?;
    header.set_sensitive(true);
    Ok(header)
}

#[cfg(test)]
#[path = "options_tests.rs"]
mod tests;
