use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{Context, Result, ensure};
use codex_exec_server::{
    ExecutorFileSystem, GetMetadataOptions, ReadFileOptions, WriteFileOptions,
};
use codex_utils_path_uri::PathUri;
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
            json!({"binary_bytes":bytes.len(),"binary_sha256":format!("{:x}",Sha256::digest(&bytes)),"metadata_size":metadata.size,"directory_names":self.names().await?}),
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
        Ok(())
    }
}
