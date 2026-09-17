use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{Context, Result, ensure};
use codex_exec_server::{
    ExecutorFileSystem, GetMetadataOptions, ReadFileOptions, WriteFileOptions,
};
use codex_utils_path_uri::PathUri;
use futures::StreamExt;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use uuid::Uuid;

pub struct RemoteFiles {
    fs: Arc<dyn ExecutorFileSystem>,
    workspace: PathBuf,
}

impl RemoteFiles {
    pub fn new(fs: Arc<dyn ExecutorFileSystem>, workspace: PathBuf) -> Self {
        Self { fs, workspace }
    }

    fn path(&self, name: &str) -> Result<PathUri> {
        PathUri::from_host_native_path(self.workspace.join(name))
            .context("encode remote filesystem path")
    }

    pub async fn write(&self, name: &str, bytes: Vec<u8>) -> Result<()> {
        self.fs
            .write_file(&self.path(name)?, bytes, WriteFileOptions::default(), None)
            .await
            .context("native file write failed")
    }

    async fn read(&self, name: &str) -> Result<Vec<u8>> {
        self.fs
            .read_file(&self.path(name)?, ReadFileOptions::default(), None)
            .await
            .context("native file read failed")
    }

    async fn streamed(&self, name: &str, limit: usize) -> Result<(Vec<u8>, bool, usize)> {
        let mut stream = self.fs.read_file_stream(&self.path(name)?, None).await?;
        let mut bytes = Vec::new();
        let mut chunks = 0;
        while let Some(chunk) = stream.next().await {
            let chunk = chunk.context("native stream read failed")?;
            ensure!(
                chunk.len() <= 1024 * 1024,
                "native chunk exceeds pinned bound"
            );
            chunks += 1;
            let remaining = limit - bytes.len();
            bytes.extend_from_slice(&chunk[..remaining.min(chunk.len())]);
            if chunk.len() > remaining {
                // Native Drop schedules close; this result is not a close receipt.
                return Ok((bytes, true, chunks));
            }
        }
        Ok((bytes, false, chunks))
    }

    async fn verify_stream(&self) -> Result<Value> {
        let expected: Vec<u8> = (0..2 * 1024 * 1024 + 37)
            .map(|index| (index % 251) as u8)
            .collect();
        let (bytes, truncated, chunks) = self.streamed("stream-binary.bin", expected.len()).await?;
        ensure!(
            bytes == expected && !truncated && chunks >= 3,
            "native multi-chunk bytes differ"
        );
        let (prefix, truncated, _) = self.streamed("stream-binary.bin", 4096).await?;
        ensure!(
            prefix == expected[..4096] && truncated,
            "native bounded prefix differs"
        );
        let (empty, truncated, _) = self.streamed("stream-empty.bin", 0).await?;
        ensure!(
            empty.is_empty() && !truncated,
            "native empty stream differs"
        );
        Ok(json!({
            "bytes": bytes.len(), "sha256": format!("{:x}", Sha256::digest(&bytes)),
            "chunks": chunks, "prefix_bytes": prefix.len(), "empty_bytes": empty.len(),
            "close_receipt_verified": false, "snapshot_consistency_verified": false
        }))
    }

    pub async fn text(&self, name: &str) -> Result<String> {
        String::from_utf8(self.read(name).await?).context("native file is not UTF-8")
    }

    pub async fn optional_text(&self, name: &str) -> Result<Option<String>> {
        match self
            .fs
            .read_file(&self.path(name)?, ReadFileOptions::default(), None)
            .await
        {
            Ok(bytes) => Ok(Some(
                String::from_utf8(bytes).context("native checkpoint is not UTF-8")?,
            )),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(error) => Err(error).context("native checkpoint read failed"),
        }
    }

    pub async fn names(&self) -> Result<Vec<String>> {
        let mut entries = self
            .fs
            .read_directory(&self.path("")?, None)
            .await
            .context("native directory listing failed")?;
        entries.sort_by(|left, right| left.file_name.cmp(&right.file_name));
        ensure!(
            entries
                .iter()
                .any(|entry| entry.file_name == "shared-binary.bin" && entry.is_file),
            "binary missing from native directory listing"
        );
        Ok(entries.into_iter().map(|entry| entry.file_name).collect())
    }

    pub async fn idle(&self, phase: &str, previous: &Value) -> Result<Value> {
        if phase == "first" {
            self.write(
                "stream-binary.bin",
                (0..2 * 1024 * 1024 + 37)
                    .map(|index| (index % 251) as u8)
                    .collect(),
            )
            .await?;
            self.write("stream-empty.bin", Vec::new()).await?;
            let seed = Uuid::now_v7();
            let bytes: Vec<u8> = (0..128 * 1024)
                .map(|index| (index % 251) as u8 ^ seed.as_bytes()[index % 16])
                .collect();
            self.write("shared-binary.bin", bytes.clone()).await?;
            ensure!(
                self.read("shared-binary.bin").await? == bytes,
                "idle binary round trip differs"
            );
        } else {
            self.verify_binary(&previous["files"]).await?;
            let expected = previous["marker"]
                .as_str()
                .context("previous marker is missing")?;
            ensure!(
                self.text("first-marker.txt").await? == format!("{expected}\n"),
                "cold file retention failed"
            );
        }
        let bytes = self.read("shared-binary.bin").await?;
        let metadata = self
            .fs
            .get_metadata(
                &self.path("shared-binary.bin")?,
                GetMetadataOptions::default(),
                None,
            )
            .await
            .context("native metadata failed")?;
        ensure!(
            bytes.len() == 128 * 1024 && metadata.size == bytes.len() as u64 && metadata.is_file,
            "native binary metadata differs"
        );
        Ok(
            json!({"binary_bytes":bytes.len(),"binary_sha256":format!("{:x}",Sha256::digest(&bytes)),"metadata_size":metadata.size,"directory_names":self.names().await?,"stream":self.verify_stream().await?}),
        )
    }

    pub async fn verify_binary(&self, proof: &Value) -> Result<()> {
        let bytes = self.read("shared-binary.bin").await?;
        ensure!(bytes.len() == 128 * 1024, "retained binary size differs");
        let metadata = self
            .fs
            .get_metadata(
                &self.path("shared-binary.bin")?,
                GetMetadataOptions::default(),
                None,
            )
            .await
            .context("native binary metadata failed")?;
        ensure!(
            metadata.size == bytes.len() as u64 && metadata.is_file,
            "native binary metadata differs"
        );
        let expected = proof["binary_sha256"]
            .as_str()
            .context("binary proof hash is missing")?;
        ensure!(
            format!("{:x}", Sha256::digest(&bytes)) == expected,
            "retained binary hash differs"
        );
        self.verify_stream().await?;
        Ok(())
    }
}
