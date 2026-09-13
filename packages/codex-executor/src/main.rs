mod options;
mod runtime;

use clap::Parser;
use codex_api::AuthProvider;
use codex_exec_server::{
    ExecServerError, RemoteEnvironmentConfig, run_remote_environment_until_shutdown,
};
use codex_http_client::{HttpClientFactory, OutboundProxyPolicy};
use http::{HeaderMap, HeaderValue};
use std::sync::Arc;

struct ExecutorAuth(HeaderValue);

impl AuthProvider for ExecutorAuth {
    fn add_auth_headers(&self, headers: &mut HeaderMap) {
        headers.insert(http::header::AUTHORIZATION, self.0.clone());
    }
}

fn main() {
    if let Err(message) = run() {
        eprintln!("{message}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), &'static str> {
    let options = options::Options::parse();
    options.validate()?;
    let paths = runtime::prepare(&options)?;

    // No threads exist yet; native helpers must use this executor's private state.
    unsafe { std::env::set_var("CODEX_HOME", &paths.codex_home) };
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(4)
        .enable_all()
        .build()
        .map_err(|_| "could not start executor runtime")?;
    runtime.block_on(async {
        let authorization = options.authorization().await?;
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .map_err(|_| "could not listen for executor shutdown")?;
        let config = RemoteEnvironmentConfig::new(
            options.remote,
            options.environment_id,
            Arc::new(ExecutorAuth(authorization)),
            HttpClientFactory::new(OutboundProxyPolicy::ReqwestDefault),
        )
        .map_err(|_| "invalid executor connection configuration")?;
        eprintln!("Starting native executor; press Ctrl-C to stop.");
        run_remote_environment_until_shutdown(config, paths.native, async {
            tokio::select! {
                _ = tokio::signal::ctrl_c() => {}
                _ = terminate.recv() => {}
            }
        })
        .await
        .map_err(|error| match error {
            ExecServerError::EnvironmentRegistryAuth(_) => {
                "registry rejected the executor credential"
            }
            ExecServerError::EnvironmentRegistryConfig(_) => {
                "native registry configuration is invalid"
            }
            ExecServerError::EnvironmentRegistryHttp { status, .. } => {
                eprintln!("Registry HTTP status: {}", status.as_u16());
                "registry rejected the native registration request"
            }
            ExecServerError::EnvironmentRegistryRequest(_) => {
                "registry connection failed; check network, TLS and proxy configuration"
            }
            ExecServerError::WebSocketConnect { .. }
            | ExecServerError::WebSocketConnectTimeout { .. }
            | ExecServerError::WebSocketConfiguration(_) => "executor WebSocket connection failed",
            _ => "native executor stopped with a protocol or execution error",
        })
    })
}
