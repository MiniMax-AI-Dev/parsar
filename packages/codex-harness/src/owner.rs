use anyhow::{Context, Result};
use std::future::Future;
use tokio_util::sync::CancellationToken;

/// Stop file admission with the runner, retaining the admitted operation's wait.
/// The file service owns its existing deadline; this does not reset that budget.
pub async fn supervise(
    runner: impl Future<Output = Result<()>>,
    files: impl Future<Output = Result<()>>,
    stopping: &CancellationToken,
) -> Result<()> {
    tokio::pin!(runner, files);
    tokio::select! {
        biased;
        result = &mut runner => {
            stopping.cancel();
            files.await.context("private file operation did not drain")?;
            result
        },
        result = &mut files => result.context("private metadata endpoint stopped"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::sync::oneshot;

    #[tokio::test]
    async fn runner_failure_waits_for_drain_and_remains_a_failure() -> Result<()> {
        let stopping = CancellationToken::new();
        let (finish, finished) = oneshot::channel();
        let operation = supervise(
            async { anyhow::bail!("runner failed") },
            async {
                stopping.cancelled().await;
                finished.await?;
                Ok(())
            },
            &stopping,
        );
        tokio::pin!(operation);
        assert!(futures::poll!(&mut operation).is_pending());
        assert!(stopping.is_cancelled());
        finish.send(()).expect("drain still owned");
        assert_eq!(operation.await.unwrap_err().to_string(), "runner failed");
        Ok(())
    }

    #[tokio::test]
    async fn unresolved_drain_is_not_clean_runner_exit() {
        let stopping = CancellationToken::new();
        let result = supervise(
            async { Ok(()) },
            async {
                stopping.cancelled().await;
                anyhow::bail!("native response unresolved")
            },
            &stopping,
        )
        .await;
        let error = result.unwrap_err();
        assert_eq!(error.to_string(), "private file operation did not drain");
        assert_eq!(error.root_cause().to_string(), "native response unresolved");
    }

    #[tokio::test]
    async fn file_failure_still_stops_the_runner() {
        let stopping = CancellationToken::new();
        let result = supervise(
            std::future::pending(),
            async { anyhow::bail!("native deadline") },
            &stopping,
        )
        .await;
        assert_eq!(
            result.unwrap_err().root_cause().to_string(),
            "native deadline"
        );
    }
}
