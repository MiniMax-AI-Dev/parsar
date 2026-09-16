use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use anyhow::{Context, Result, anyhow, ensure};
use codex_app_server_protocol::{ServerNotification, ThreadItem};
use codex_shell_command::parse_command::extract_shell_command;
use serde_json::{Value, json};
use tokio::time::sleep;

#[derive(Clone, Default)]
struct Observed {
    failure: Option<&'static str>,
    turns_started: Vec<(String, String)>,
    turns_completed: Vec<(String, String, Value)>,
    commands_started: Vec<(String, String, String)>,
    commands_completed: Vec<(String, String, Value)>,
    answer: String,
    events: Vec<Value>,
    event_bytes: usize,
    event_count: usize,
}

impl Observed {
    fn record(&mut self, notification: ServerNotification) -> Result<()> {
        self.event_count += 1;
        ensure!(
            self.event_count <= 10_000,
            "native event count exceeded fixture bound"
        );
        let relevant = match &notification {
            ServerNotification::TurnStarted(event) => {
                self.turns_started
                    .push((event.thread_id.clone(), event.turn.id.clone()));
                true
            }
            ServerNotification::TurnCompleted(event) => {
                self.turns_completed.push((
                    event.thread_id.clone(),
                    event.turn.id.clone(),
                    serde_json::to_value(&event.turn)?,
                ));
                true
            }
            ServerNotification::ItemStarted(event) => {
                if let ThreadItem::CommandExecution { id, .. } = &event.item {
                    self.commands_started.push((
                        event.thread_id.clone(),
                        event.turn_id.clone(),
                        id.clone(),
                    ));
                    true
                } else {
                    false
                }
            }
            ServerNotification::ItemCompleted(event) => match &event.item {
                ThreadItem::CommandExecution { .. } => {
                    self.commands_completed.push((
                        event.thread_id.clone(),
                        event.turn_id.clone(),
                        serde_json::to_value(&event.item)?,
                    ));
                    true
                }
                ThreadItem::AgentMessage { text, phase, .. } => {
                    let phase = serde_json::to_value(phase)?;
                    if phase.is_null() || phase == "final_answer" {
                        ensure!(
                            self.answer.len() + text.len() <= 256 * 1024,
                            "answer exceeded fixture bound"
                        );
                        self.answer.push_str(text);
                        self.answer.push('\n');
                    }
                    true
                }
                _ => false,
            },
            _ => false,
        };
        ensure!(
            self.turns_started.len() <= 1
                && self.turns_completed.len() <= 1
                && self.commands_started.len() <= 1
                && self.commands_completed.len() <= 1,
            "unexpected additional Turn or command"
        );
        if relevant {
            let value = serde_json::to_value(notification)?;
            self.event_bytes += serde_json::to_vec(&value)?.len();
            ensure!(
                self.event_bytes <= 4 * 1024 * 1024,
                "native observations exceeded fixture byte bound"
            );
            self.events.push(value);
        }
        Ok(())
    }
}

pub struct Evidence {
    pub answer: String,
    pub command: Value,
    pub events: Vec<Value>,
}

#[derive(Clone, Default)]
pub struct Observations {
    state: Arc<Mutex<Observed>>,
}

impl Observations {
    pub fn record(&self, notification: ServerNotification) -> Result<()> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| anyhow!("observation lock poisoned"))?;
        if state.failure.is_none() && state.record(notification).is_err() {
            state.failure = Some("native observation bounds or counts failed");
        }
        Ok(())
    }

    pub fn fail(&self, reason: &'static str) -> Result<()> {
        self.state
            .lock()
            .map_err(|_| anyhow!("observation lock poisoned"))?
            .failure = Some(reason);
        Ok(())
    }

    fn snapshot(&self) -> Result<Observed> {
        Ok(self
            .state
            .lock()
            .map_err(|_| anyhow!("observation lock poisoned"))?
            .clone())
    }

    pub fn events(&self) -> Result<Vec<Value>> {
        self.healthy()?;
        Ok(self.snapshot()?.events)
    }

    pub fn healthy(&self) -> Result<()> {
        let state = self
            .state
            .lock()
            .map_err(|_| anyhow!("observation lock poisoned"))?;
        ensure!(
            state.failure.is_none(),
            "{}",
            state.failure.unwrap_or("native observation failed")
        );
        Ok(())
    }

    pub fn require_active(&self, thread: &str, turn: &str) -> Result<()> {
        self.healthy()?;
        let state = self.snapshot()?;
        ensure!(
            state.turns_started == vec![(thread.to_owned(), turn.to_owned())]
                && state.turns_completed.is_empty(),
            "native Turn is not observed active"
        );
        ensure!(
            state.commands_started.len() == 1 && state.commands_completed.is_empty(),
            "native command is not observed active"
        );
        ensure!(
            state.commands_started[0].0 == thread && state.commands_started[0].1 == turn,
            "active command belongs to another Turn"
        );
        Ok(())
    }

    pub fn require_unfinished(&self) -> Result<()> {
        self.healthy()?;
        ensure!(
            self.snapshot()?.turns_completed.is_empty(),
            "native Turn completed before command heartbeat"
        );
        Ok(())
    }

    pub async fn wait_active(&self, thread: &str, turn: &str) -> Result<()> {
        loop {
            self.healthy()?;
            let state = self.snapshot()?;
            ensure!(
                state.turns_completed.is_empty() && state.commands_completed.is_empty(),
                "command completed before active checkpoint"
            );
            if state.turns_started.len() == 1 && state.commands_started.len() == 1 {
                return self.require_active(thread, turn);
            }
            sleep(Duration::from_millis(25)).await;
        }
    }

    pub async fn wait_completed(&self) -> Result<()> {
        loop {
            self.healthy()?;
            if !self.snapshot()?.turns_completed.is_empty() {
                return Ok(());
            }
            sleep(Duration::from_millis(100)).await;
        }
    }

    pub fn validate(
        &self,
        thread: &str,
        turn: &str,
        phase: &str,
        workspace: &Path,
        marker: &str,
        history: &str,
    ) -> Result<Evidence> {
        self.healthy()?;
        let state = self.snapshot()?;
        ensure!(
            state.turns_started == vec![(thread.to_owned(), turn.to_owned())]
                && state.turns_completed.len() == 1,
            "native Turn lifecycle missing"
        );
        let completed = &state.turns_completed[0];
        ensure!(
            completed.0 == thread && completed.1 == turn && completed.2["status"] == "completed",
            "native Turn did not complete successfully"
        );
        ensure!(
            state.commands_started.len() == 1 && state.commands_completed.len() == 1,
            "native command lifecycle missing"
        );
        let started = &state.commands_started[0];
        let completed = &state.commands_completed[0];
        let item = &completed.2;
        ensure!(
            started.0 == thread
                && started.1 == turn
                && completed.0 == thread
                && completed.1 == turn
                && item["id"] == started.2,
            "command lifecycle identities differ"
        );
        let command = item["command"]
            .as_str()
            .context("native command text missing")?;
        ensure!(
            is_gate_command(command, phase),
            "model executed an unexpected command"
        );
        let cwd = workspace.to_str().context("workspace encoding")?;
        ensure!(
            item["cwd"] == cwd && item["exitCode"] == 7,
            "native command cwd or exit differs"
        );
        let output = item["aggregatedOutput"]
            .as_str()
            .context("native command output missing")?;
        ensure!(
            output.contains(marker)
                && output.contains(&format!("remote-stdout:{phase}"))
                && output.contains(&format!("remote-stderr:{phase}")),
            "native command output observations missing"
        );
        ensure!(
            !command.contains(history) && !output.contains(history),
            "prompt-only history leaked into command or files"
        );
        ensure!(
            state.answer.contains(marker) && state.answer.contains(history),
            "native final answer did not recall marker and history"
        );
        Ok(Evidence {
            answer: state.answer,
            command: json!({"id":item["id"],"command":command,"cwd":cwd,"aggregated_output":output,"exit_code":7,"started":true,"completed":true}),
            events: state.events,
        })
    }
}

pub(super) fn is_gate_command(command: &str, phase: &str) -> bool {
    let Some(argv) = shlex::split(command) else {
        return false;
    };
    argv == ["./shared-gate.sh", phase]
        || extract_shell_command(&argv)
            .is_some_and(|(_, script)| script == format!("./shared-gate.sh {phase}"))
}

#[cfg(test)]
mod tests {
    use super::is_gate_command;

    #[test]
    fn exact_gate_accepts_native_presentation_only() {
        assert!(is_gate_command("./shared-gate.sh first", "first"));
        assert!(is_gate_command(
            "/bin/bash -lc './shared-gate.sh first'",
            "first"
        ));
        assert!(!is_gate_command(
            "/bin/bash -lc './shared-gate.sh first; echo invented'",
            "first"
        ));
        assert!(!is_gate_command("./shared-gate.sh fresh", "first"));
        assert!(!is_gate_command(
            "./shared-gate.sh first && echo invented",
            "first"
        ));
    }
}
