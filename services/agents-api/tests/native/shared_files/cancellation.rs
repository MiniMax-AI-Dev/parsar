use std::path::Path;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use serde_json::{Value, json};
use tokio::time::{sleep, timeout};
use uuid::Uuid;

use crate::files::RemoteFiles;
use crate::observations::is_gate_command;
use crate::probe::{field, wait_checkpoint};
use crate::runtime::Runtime;

pub async fn exercise(
    root: &Path,
    workspace: &Path,
    fs: &RemoteFiles,
    client: &Runtime,
) -> Result<Value> {
    let previous = read_proof(&root.join("first.json")).await?;
    let thread = field(&previous, "native_thread_id")?;
    let mut files = fs.idle("cancel", &previous).await?;
    let resumed = client
        .request(
            1,
            "thread/resume",
            json!({"threadId":thread,"cwd":workspace}),
        )
        .await?;
    ensure!(
        resumed["thread"]["id"] == thread,
        "cancel resume changed native thread"
    );
    let prompt = "Execute exactly one command: `./shared-gate.sh cancel`. Pass that entire command with its required cancel argument to the native exec_command tool. It waits on a bounded fixture gate and will be interrupted externally. Keep waiting with native polling if needed. Never rerun it or execute another command, and do not open its gate or read/write files yourself.";
    let response = client.request(2, "turn/start", json!({"threadId":thread,"input":[{"type":"text","text":prompt}],"environments":[{"environmentId":"remote","cwd":workspace}]})).await?;
    let turn = field(&response["turn"], "id")?;
    let heartbeat = timeout(Duration::from_secs(150), async {
        loop {
            client.observations.require_unfinished()?;
            if let Some(heartbeat) = fs.optional_text("cancel.heartbeat").await? {
                ensure!(!heartbeat.trim().is_empty(), "empty cancellation heartbeat");
                client.observations.wait_active(thread, turn).await?;
                return Ok::<_, anyhow::Error>(heartbeat);
            }
            sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("cancel command heartbeat timed out")??;
    wait_checkpoint(&root.join("cancel-active-observed"), client).await?;
    client.observations.require_active(thread, turn)?;
    let started = command_started(&client.observations.events()?, thread, turn, workspace)?;
    fs.verify_binary(&files).await?;
    let active_directory = fs.names().await?;
    require_closed_gate(fs).await?;
    let marker = format!("shared-marker-{}", Uuid::now_v7());
    fs.write("cancel-marker.txt", format!("{marker}\n").into_bytes())
        .await?;
    ensure!(
        fs.text("cancel-marker.txt").await? == format!("{marker}\n"),
        "active cancel marker differs"
    );
    client.observations.require_active(thread, turn)?;

    // The native acknowledgement can also race natural completion. The separate
    // terminal notification must identify this Turn with status interrupted.
    let interrupt = client
        .request(
            3,
            "turn/interrupt",
            json!({"threadId":thread,"turnId":turn}),
        )
        .await?;
    ensure!(
        interrupt == json!({}),
        "unexpected native interrupt response"
    );
    client.observations.wait_completed().await?;
    let completed_turn = interrupted_turn(&client.observations.events()?, thread, turn)?;
    let before = client
        .request(
            4,
            "thread/backgroundTerminals/list",
            json!({"threadId":thread,"limit":10}),
        )
        .await?;
    let termination = if let Some(target) = select_target(&before, &started)? {
        let process = field(target, "processId")?;
        let response = client
            .request(
                5,
                "thread/backgroundTerminals/terminate",
                json!({"threadId":thread,"processId":process}),
            )
            .await?;
        ensure!(
            response["terminated"] == true,
            "native targeted termination was not confirmed"
        );
        json!({"request_id":5,"process_id":process,"response":response})
    } else {
        Value::Null
    };
    let after = client
        .request(
            6,
            "thread/backgroundTerminals/list",
            json!({"threadId":thread,"limit":10}),
        )
        .await?;
    ensure!(
        select_target(&after, &started)?.is_none(),
        "cancelled native target remains listed"
    );
    // This is an observed native target state, not proof of OS quiescence. Go
    // independently checks the fixture PID while this owner is still alive.
    fs.verify_binary(&files).await?;
    ensure!(
        fs.text("cancel-marker.txt").await? == format!("{marker}\n"),
        "post-cancel marker differs"
    );
    require_closed_gate(fs).await?;
    ensure!(
        fs.text("shared-gate-count")
            .await?
            .lines()
            .collect::<Vec<_>>()
            == ["first", "cancel"],
        "cancel command was repeated"
    );
    ensure!(
        fs.text("cancel.cwd").await?.trim() == workspace.to_str().context("workspace encoding")?,
        "cancel command cwd differs"
    );
    files["active_heartbeat"] = json!(heartbeat);
    files["active_binary_verified"] = json!(true);
    files["active_directory_names"] = json!(active_directory);
    files["directory_names"] = json!(fs.names().await?);
    let mut proof = json!({
        "phase":"cancel", "native_thread_id":thread, "native_turn_id":turn,
        "marker":marker,"history_value":field(&previous,"history_value")?,"files":files,
        "shutdown_completed":false,"no_lagged_observed":true,
        "cancellation":{
            "interrupt":{"request_id":3,"response":interrupt},
            "turn_completed":completed_turn,"command_started":started,"command_completed":null,
            "background_before":before,"termination":termination,"background_after":after,
            "files_after":{"binary_verified":true,"marker_verified":true},
            "observation_scope":"Typed native notification bodies observed before the snapshot; absent completion is not reconstructed."
        }
    });
    refresh(&mut proof, client)?;
    crate::runtime::annotate(&mut proof);
    Ok(proof)
}

async fn require_closed_gate(fs: &RemoteFiles) -> Result<()> {
    ensure!(
        fs.optional_text("cancel.release").await?.is_none(),
        "cancel gate was released"
    );
    ensure!(
        fs.optional_text("cancel-artifact.txt").await?.is_none(),
        "cancel command reached placement"
    );
    Ok(())
}

pub fn refresh(proof: &mut Value, client: &Runtime) -> Result<()> {
    let events = client.observations.events()?;
    let thread = field(proof, "native_thread_id")?;
    let turn = field(proof, "native_turn_id")?;
    let terminal = interrupted_turn(&events, thread, turn)?;
    let completed = notification(&events, "item/completed", true, false)?;
    if !completed.is_null() {
        ensure!(
            completed["threadId"] == thread
                && completed["turnId"] == turn
                && completed["item"]["id"]
                    == proof["cancellation"]["command_started"]["item"]["id"],
            "cancel completion belongs to another native command"
        );
        let process = &completed["item"]["processId"];
        ensure!(
            process.is_null()
                || process == &proof["cancellation"]["command_started"]["item"]["processId"],
            "cancel completion process changed"
        );
    }
    proof["command_completed_count"] = json!(usize::from(!completed.is_null()));
    proof["cancellation"]["command_completed"] = completed;
    proof["cancellation"]["turn_completed"] = terminal;
    proof["turn_started_count"] = json!(1);
    proof["turn_completed_count"] = json!(1);
    proof["command_started_count"] = json!(1);
    proof["events"] = json!(events);
    Ok(())
}

pub async fn recover(
    root: &Path,
    thread: &str,
    fs: &RemoteFiles,
    client: &Runtime,
) -> Result<Option<Value>> {
    let path = root.join("cancel.json");
    if !tokio::fs::try_exists(&path)
        .await
        .context("inspect cancellation proof")?
    {
        return Ok(None);
    }
    let cancelled = read_proof(&path).await?;
    ensure!(
        cancelled["native_thread_id"] == thread && cancelled["shutdown_completed"] == true,
        "cancel owner did not finish on the resumed thread"
    );
    let turns = client
        .request(
            3,
            "thread/turns/list",
            json!({"threadId":thread,"limit":10,"sortDirection":"desc"}),
        )
        .await?;
    validate_recovery(&turns, field(&cancelled, "native_turn_id")?)?;
    fs.verify_binary(&cancelled["files"]).await?;
    ensure!(
        fs.text("cancel-marker.txt").await? == format!("{}\n", field(&cancelled, "marker")?),
        "fresh cancel marker differs"
    );
    Ok(Some(json!({"native_turns":turns,"files_verified":true})))
}

async fn read_proof(path: &Path) -> Result<Value> {
    serde_json::from_slice(
        &tokio::fs::read(path)
            .await
            .context("read prior native proof")?,
    )
    .context("decode prior native proof")
}

fn notification(
    events: &[Value],
    method: &str,
    command_only: bool,
    required: bool,
) -> Result<Value> {
    let matching: Vec<_> = events
        .iter()
        .filter(|event| {
            event["method"] == method
                && (!command_only || event["params"]["item"]["type"] == "commandExecution")
        })
        .collect();
    ensure!(
        matching.len() <= 1 && (!required || matching.len() == 1),
        "native cancellation lifecycle count differs"
    );
    Ok(matching
        .first()
        .map(|event| event["params"].clone())
        .unwrap_or(Value::Null))
}

fn command_started(events: &[Value], thread: &str, turn: &str, workspace: &Path) -> Result<Value> {
    let started = notification(events, "item/started", true, true)?;
    ensure!(
        started["threadId"] == thread && started["turnId"] == turn,
        "cancel command belongs to another Turn"
    );
    let item = &started["item"];
    field(item, "id")?;
    field(item, "processId")?;
    ensure!(
        item["cwd"] == workspace.to_str().context("workspace encoding")?
            && is_gate_command(field(item, "command")?, "cancel"),
        "unexpected cancel command or cwd"
    );
    Ok(started)
}

fn interrupted_turn(events: &[Value], thread: &str, turn: &str) -> Result<Value> {
    let started = notification(events, "turn/started", false, true)?;
    ensure!(
        started["threadId"] == thread && started["turn"]["id"] == turn,
        "cancel Turn start identity differs"
    );
    let terminal = notification(events, "turn/completed", false, true)?;
    ensure!(
        terminal["threadId"] == thread
            && terminal["turn"]["id"] == turn
            && terminal["turn"]["status"] == "interrupted",
        "native cancel Turn did not end interrupted"
    );
    Ok(terminal)
}

fn select_target<'a>(listed: &'a Value, started: &Value) -> Result<Option<&'a Value>> {
    ensure!(
        listed["nextCursor"].is_null(),
        "unexpected background-terminal pagination"
    );
    let data = listed["data"]
        .as_array()
        .context("native background-terminal list missing")?;
    ensure!(data.len() <= 1, "ambiguous native background terminals");
    let Some(target) = data.first() else {
        return Ok(None);
    };
    let item = &started["item"];
    ensure!(
        target["itemId"] == item["id"]
            && target["processId"] == item["processId"]
            && target["cwd"] == item["cwd"]
            && is_gate_command(field(target, "command")?, "cancel"),
        "background terminal is not the observed current-Turn command"
    );
    Ok(Some(target))
}

fn validate_recovery(turns: &Value, cancelled_turn: &str) -> Result<()> {
    ensure!(
        turns["nextCursor"].is_null(),
        "unexpected native recovery pagination"
    );
    let data = turns["data"]
        .as_array()
        .context("native recovery turns missing")?;
    ensure!(
        data.len() == 2,
        "native recovery did not retain both prior Turns"
    );
    let matching: Vec<_> = data
        .iter()
        .filter(|turn| turn["id"] == cancelled_turn)
        .collect();
    ensure!(
        matching.len() == 1 && matching[0]["status"] == "interrupted",
        "native history did not retain the cancelled Turn"
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{command_started, interrupted_turn, select_target, validate_recovery};
    use serde_json::json;
    use std::path::Path;

    #[test]
    fn cancellation_target_requires_current_turn_and_process() -> anyhow::Result<()> {
        let mut event = json!({"method":"item/started","params":{"threadId":"thread","turnId":"cancel","item":{"type":"commandExecution","id":"item","processId":"42","cwd":"/remote","command":"/bin/sh -lc './shared-gate.sh cancel'"}}});
        let started = command_started(&[event.clone()], "thread", "cancel", Path::new("/remote"))?;
        let mut list = json!({"data":[{"itemId":"item","processId":"42","cwd":"/remote","command":"./shared-gate.sh cancel"}],"nextCursor":null});
        assert!(select_target(&list, &started)?.is_some());
        list["data"][0]["processId"] = json!("43");
        assert!(select_target(&list, &started).is_err());
        list["data"][0]["processId"] = json!("42");
        list["data"][0]["itemId"] = json!("other");
        assert!(select_target(&list, &started).is_err());
        event["params"]["turnId"] = json!("previous");
        assert!(
            command_started(&[event.clone()], "thread", "cancel", Path::new("/remote")).is_err()
        );
        event["params"]["turnId"] = json!("cancel");
        event["params"]["item"]["processId"] = json!(null);
        assert!(command_started(&[event], "thread", "cancel", Path::new("/remote")).is_err());
        Ok(())
    }

    #[test]
    fn acknowledgement_does_not_substitute_for_interrupted_turn() -> anyhow::Result<()> {
        let start =
            json!({"method":"turn/started","params":{"threadId":"thread","turn":{"id":"cancel"}}});
        let mut terminal = json!({"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"cancel","status":"interrupted"}}});
        interrupted_turn(&[start.clone(), terminal.clone()], "thread", "cancel")?;
        terminal["params"]["turn"]["status"] = json!("completed");
        assert!(interrupted_turn(&[start, terminal], "thread", "cancel").is_err());
        Ok(())
    }

    #[test]
    fn recovery_requires_exact_cancelled_turn_once() -> anyhow::Result<()> {
        let mut page = json!({"data":[{"id":"first","status":"completed"},{"id":"cancel","status":"interrupted"}],"nextCursor":null});
        validate_recovery(&page, "cancel")?;
        assert!(validate_recovery(&page, "other").is_err());
        page["data"][1]["status"] = json!("completed");
        assert!(validate_recovery(&page, "cancel").is_err());
        page["data"] =
            json!([{"id":"cancel","status":"interrupted"},{"id":"cancel","status":"interrupted"}]);
        assert!(validate_recovery(&page, "cancel").is_err());
        Ok(())
    }
}
