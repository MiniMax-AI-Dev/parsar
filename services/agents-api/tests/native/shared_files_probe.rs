use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use codex_exec_server::EnvironmentManager;
use serde_json::{Value, json};
use tokio::time::{sleep, timeout};
use uuid::Uuid;

#[path = "shared_files/files.rs"]
mod files;
#[path = "shared_files/runtime.rs"]
mod runtime;

const PHASE_TIMEOUT: Duration = Duration::from_secs(240);
// Match pinned codex arg0/src/lib.rs and async-utils/src/lib.rs for native futures.
const NATIVE_STACK_BYTES: usize = 16 * 1024 * 1024;

fn main() -> std::process::ExitCode {
    match run_native_runtime() {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(error) => {
            // Display only the safe outer context, not upstream connection/auth error chains.
            eprintln!("shared-files probe failed: {error}");
            std::process::ExitCode::FAILURE
        }
    }
}

fn run_native_runtime() -> Result<()> {
    std::thread::Builder::new()
        .name("shared-files-main".to_owned())
        .stack_size(NATIVE_STACK_BYTES)
        .spawn(|| {
            tokio::runtime::Builder::new_multi_thread()
                .enable_all()
                .worker_threads(4)
                .thread_stack_size(NATIVE_STACK_BYTES)
                .build()
                .context("native runtime construction failed")?
                .block_on(run())
        })
        .context("native main thread construction failed")?
        .join()
        .map_err(|_| anyhow::anyhow!("native main thread panicked"))?
}

async fn run() -> Result<()> {
    let phase = std::env::args().nth(1).context("phase is required")?;
    ensure!(matches!(phase.as_str(), "first" | "fresh"), "invalid phase");
    let root = required_path("PARSAR_SHARED_FILES_ROOT")?;
    ensure!(
        root.components().any(|part| part.as_os_str() == ".parsar"),
        "proof root must be under .parsar"
    );
    let workspace = required_path("PARSAR_SHARED_FILES_WORKSPACE")?;
    ensure!(
        !workspace.exists(),
        "executor workspace exists on harness host"
    );
    let binary = required_path("PARSAR_CODEX_BINARY")?;
    ensure!(binary.is_file(), "native resource binary is missing");
    ensure!(
        !std::env::var("PARSAR_PROBE_MODEL_KEY")
            .unwrap_or_default()
            .trim()
            .is_empty(),
        "explicit model key is missing"
    );
    ensure!(
        std::env::var_os("CODEX_API_KEY").is_none() && std::env::var_os("OPENAI_API_KEY").is_none(),
        "ambient provider authentication is present"
    );
    for name in [
        "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL",
        "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID",
        "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN",
    ] {
        ensure!(
            !std::env::var(name).unwrap_or_default().is_empty(),
            "explicit Noise configuration is missing"
        );
    }
    tokio::fs::create_dir_all(root.join("harness"))
        .await
        .context("create harness directory")?;
    let (args, manager) = runtime::configuration(&root, binary).await?;
    ensure!(
        manager.try_local_environment().is_none(),
        "local fallback is configured"
    );
    let environment = manager
        .default_environment()
        .context("remote Environment is absent")?;
    ensure!(environment.is_remote(), "Environment is not remote");
    timeout(Duration::from_secs(45), environment.wait_until_ready())
        .await
        .context("remote readiness timed out")?
        .context("remote readiness failed")?;
    let fs = files::RemoteFiles::new(environment.get_filesystem(), workspace.clone());
    let client = runtime::Runtime::start(args).await?;
    let result = timeout(
        PHASE_TIMEOUT,
        exercise(&root, &workspace, &phase, &fs, &client, &manager),
    )
    .await
    .context("shared-files phase timed out")
    .and_then(|result| result);
    let result = async {
        let proof = result?;
        save(&root, &format!("{phase}-ready.json"), &proof).await?;
        wait_checkpoint(&root.join(format!("{phase}-release")), &client).await?;
        Ok::<_, anyhow::Error>(proof)
    }
    .await;
    let stopped = client.shutdown().await;
    drop(fs);
    drop(environment);
    drop(manager);
    stopped?;
    let mut proof = result?;
    proof["shutdown_completed"] = json!(true);
    save(&root, &format!("{phase}.json"), &proof).await
}

async fn exercise(
    root: &Path,
    workspace: &Path,
    phase: &str,
    fs: &files::RemoteFiles,
    client: &runtime::Runtime,
    manager: &Arc<EnvironmentManager>,
) -> Result<Value> {
    let previous: Value = if phase == "fresh" {
        serde_json::from_slice(
            &tokio::fs::read(root.join("first.json"))
                .await
                .context("read first proof")?,
        )
        .context("decode first proof")?
    } else {
        Value::Null
    };
    let marker = format!("shared-marker-{}", Uuid::now_v7());
    let history = if phase == "first" {
        format!("history-only-{}", Uuid::now_v7())
    } else {
        field(&previous, "history_value")?.to_owned()
    };
    let mut file_proof = fs.idle(phase, &previous).await?;
    let selection = json!([{"environmentId":"remote", "cwd":workspace}]);
    let thread_response = if phase == "first" {
        client
            .request(
                1,
                "thread/start",
                json!({"cwd":workspace,"environments":selection,"ephemeral":false}),
            )
            .await?
    } else {
        client
            .request(
                1,
                "thread/resume",
                json!({"threadId":field(&previous,"native_thread_id")?,"cwd":workspace}),
            )
            .await?
    };
    let thread_id = field(&thread_response["thread"], "id")?.to_owned();
    if phase == "fresh" {
        ensure!(
            thread_id == field(&previous, "native_thread_id")?,
            "cold resume changed thread identity"
        );
    }
    let memory_instruction = if phase == "first" {
        format!(
            "Remember this prompt-only history value: {history}. Never put that value in a command, tool argument, or file."
        )
    } else {
        "Recall the prompt-only history value from our previous turn. It is not in any workspace file; do not search for it.".to_owned()
    };
    let prompt = format!(
        "{memory_instruction} Execute exactly one command: `./shared-gate.sh {phase}`. The `{phase}` argument is required; pass the entire command including this argument as the exec_command cmd value. Do not omit or change the argument. It waits on a bounded fixture gate and intentionally exits 7 after printing a new random marker, remote stdout and remote stderr. Let it finish; use native polling if needed, but never rerun it or execute any other command. Do not open the gate or read/write any files yourself. Then give one final answer containing both the exact newly printed marker and the prompt-only history value. Do not guess the marker or repair the intentional exit status."
    );
    let started = client.request(2, "turn/start", json!({"threadId":thread_id,"input":[{"type":"text","text":prompt}],"environments":selection})).await?;
    let turn_id = field(&started["turn"], "id")?.to_owned();
    let heartbeat = timeout(Duration::from_secs(150), async {
        loop {
            client.require_unfinished()?;
            if let Some(value) = fs.optional_text(&format!("{phase}.heartbeat")).await? {
                ensure!(!value.trim().is_empty(), "empty active heartbeat");
                client.wait_active(&thread_id, &turn_id).await?;
                break Ok::<_, anyhow::Error>(value);
            }
            sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("real command heartbeat timed out")??;
    wait_checkpoint(&root.join(format!("{phase}-active-observed")), client).await?;
    client.require_active(&thread_id, &turn_id)?;
    fs.verify_binary(&file_proof).await?;
    let active_directory = fs.names().await?;
    ensure!(
        fs.optional_text(&format!("{phase}.release"))
            .await?
            .is_none(),
        "remote gate was already released"
    );
    fs.write(
        &format!("{phase}-marker.txt"),
        format!("{marker}\n").into_bytes(),
    )
    .await?;
    ensure!(
        fs.text(&format!("{phase}-marker.txt")).await? == format!("{marker}\n"),
        "active direct file round trip differs"
    );
    fs.write(&format!("{phase}.release"), b"release\n".to_vec())
        .await?;
    client.wait_completed().await?;
    let observation = client.validate(&thread_id, &turn_id, phase, workspace, &marker, &history)?;
    ensure!(
        fs.text(&format!("{phase}.cwd")).await?.trim()
            == workspace.to_str().context("workspace path encoding")?,
        "remote command cwd differs"
    );
    let artifact = fs.text(&format!("{phase}-artifact.txt")).await?;
    ensure!(
        artifact == format!("{marker}\n"),
        "command artifact differs from direct file marker"
    );
    let counts = fs.text("shared-gate-count").await?;
    let expected = if phase == "first" {
        vec!["first"]
    } else {
        vec!["first", "fresh"]
    };
    ensure!(
        counts.lines().collect::<Vec<_>>() == expected,
        "real command gate ran more than once"
    );
    fs.verify_binary(&file_proof).await?;
    file_proof["active_heartbeat"] = json!(heartbeat);
    file_proof["active_binary_verified"] = json!(true);
    file_proof["active_directory_names"] = json!(active_directory);
    file_proof["artifact"] = json!(artifact);
    file_proof["directory_names"] = json!(fs.names().await?);
    ensure!(
        manager.try_local_environment().is_none(),
        "local fallback appeared"
    );
    Ok(json!({
        "status":"shared_native_files_characterized_with_blockers", "phase":phase,
        "native_thread_id":thread_id,"native_turn_id":turn_id,"marker":marker,"history_value":history,
        "answer":observation.answer,"command":observation.command,"files":file_proof,
        "turn_started_count":1,"turn_completed_count":1,"command_started_count":1,"command_completed_count":1,
        "events":observation.events,"no_lagged_observed":true,"shutdown_completed":false,
        "limitations":[
            "Pinned in-process queues can silently drop non-required notifications; only this bounded workflow's required observations were checked. Production lossless delivery remains blocked.",
            "This is typed in-process embedding, not raw stdio compatibility or a production daemon integration.",
            "Upstream shutdown may abort internal tasks; returned shutdown is not proof of executor OS quiescence.",
            "Direct native filesystem checks do not establish authorization, workspace confinement, public pagination or file_id semantics."
        ]
    }))
}

async fn wait_checkpoint(path: &Path, client: &runtime::Runtime) -> Result<()> {
    timeout(Duration::from_secs(90), async {
        loop {
            client.healthy()?;
            if tokio::fs::try_exists(path)
                .await
                .context("read host checkpoint")?
            {
                return Ok::<_, anyhow::Error>(());
            }
            sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("fixture checkpoint timed out")?
}

fn required_path(name: &str) -> Result<PathBuf> {
    let path = PathBuf::from(std::env::var(name).with_context(|| format!("{name} is required"))?);
    ensure!(path.is_absolute(), "{name} must be absolute");
    Ok(path)
}

fn field<'a>(value: &'a Value, key: &str) -> Result<&'a str> {
    value[key]
        .as_str()
        .filter(|value| !value.is_empty())
        .with_context(|| format!("missing proof field {key}"))
}

async fn save(root: &Path, name: &str, value: &Value) -> Result<()> {
    let temporary = root.join(format!("{name}.tmp"));
    tokio::fs::write(
        &temporary,
        serde_json::to_vec_pretty(value).context("encode proof")?,
    )
    .await
    .context("write proof")?;
    tokio::fs::rename(temporary, root.join(name))
        .await
        .context("publish proof")
}
