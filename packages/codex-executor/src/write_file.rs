use crate::workspace_path::{anchor, directory};
use rustix::fs::{AtFlags, FileType, Mode, OFlags, fstat, openat, renameat, statat, unlinkat};
use sha2::{Digest, Sha256};
use std::fs::File;
use std::io::{self, Read, Write};
use std::os::fd::OwnedFd;
use std::path::Path;

pub(crate) const MAX_BYTES: u64 = 50 * 1024 * 1024;

#[derive(Debug)]
pub(crate) struct Failure {
    pub committed: bool,
    pub error: io::Error,
}

impl From<io::Error> for Failure {
    fn from(error: io::Error) -> Self {
        Self {
            committed: false,
            error,
        }
    }
}

pub(crate) fn install(
    root: &Path,
    relative: &str,
    size: u64,
    mut input: impl Read,
    staging_root: &Path,
) -> Result<(), Failure> {
    if size > MAX_BYTES
        || staging_root.starts_with(root)
        || root.starts_with(staging_root)
        || relative.is_empty()
        || relative.len() > 4096
        || relative
            .split('/')
            .any(|p| p.is_empty() || p == "." || p == ".." || p.contains(['\\', '\0', '\r', '\n']))
    {
        return Err(io::Error::from(io::ErrorKind::InvalidInput).into());
    }
    let (parent, leaf) = relative.rsplit_once('/').unwrap_or(("", relative));
    let root = anchor(root)?;
    let parent = directory(&root, parent)?;
    let staging_parent = directory(&anchor(staging_root)?, "")?;
    if fstat(&parent).map_err(io::Error::from)?.st_dev
        != fstat(&staging_parent).map_err(io::Error::from)?.st_dev
    {
        return Err(io::Error::from(io::ErrorKind::InvalidInput).into());
    }
    match statat(&parent, leaf, AtFlags::SYMLINK_NOFOLLOW) {
        Ok(metadata) if FileType::from_raw_mode(metadata.st_mode) == FileType::RegularFile => (),
        Ok(_) => return Err(io::Error::from(io::ErrorKind::InvalidInput).into()),
        Err(rustix::io::Errno::NOENT) => (),
        Err(error) => return Err(io::Error::from(error).into()),
    }
    let staging = Staging::new(&staging_parent)?;
    let mut file = &staging.file;
    let mut digest = Sha256::new();
    let mut remaining = size;
    let mut buffer = [0u8; 64 * 1024];
    while remaining != 0 {
        let count = remaining.min(buffer.len() as u64) as usize;
        input.read_exact(&mut buffer[..count])?;
        file.write_all(&buffer[..count])?;
        digest.update(&buffer[..count]);
        remaining -= count as u64;
    }
    // Native process/write has no stdin-close operation; the digest ends one frame.
    let mut trailer = [0u8; 32];
    input.read_exact(&mut trailer)?;
    if digest.finalize().as_slice() != trailer {
        return Err(io::Error::from(io::ErrorKind::InvalidData).into());
    }
    staging.file.sync_all()?;
    staging.persist(&parent, leaf)?;
    File::from(parent).sync_all().map_err(|error| Failure {
        committed: true,
        error,
    })?;
    File::from(staging_parent)
        .sync_all()
        .map_err(|error| Failure {
            committed: true,
            error,
        })?;
    Ok(())
}

struct Staging<'a> {
    parent: &'a OwnedFd,
    name: String,
    file: File,
    committed: bool,
}

impl<'a> Staging<'a> {
    fn new(parent: &'a OwnedFd) -> io::Result<Self> {
        let name = format!(".parsar-upload-{}", uuid::Uuid::new_v4());
        let file = openat(
            parent,
            name.as_str(),
            OFlags::WRONLY | OFlags::CREATE | OFlags::EXCL | OFlags::NOFOLLOW | OFlags::CLOEXEC,
            Mode::from_raw_mode(0o600),
        )?;
        Ok(Self {
            parent,
            name,
            file: File::from(file),
            committed: false,
        })
    }

    fn persist(mut self, destination: &OwnedFd, leaf: &str) -> io::Result<()> {
        renameat(self.parent, self.name.as_str(), destination, leaf)?;
        self.committed = true;
        Ok(())
    }
}

impl Drop for Staging<'_> {
    fn drop(&mut self) {
        if !self.committed {
            let _ = unlinkat(self.parent, self.name.as_str(), AtFlags::empty());
        }
    }
}

#[cfg(test)]
#[path = "write_file_tests.rs"]
mod tests;
