use crate::{directory::observe, workspace_path};
use rustix::fs::{FileType, Mode, OFlags, Stat, fstat, openat};
use std::fs::File;
use std::io::{self, Read, Write};
use std::os::fd::OwnedFd;
use std::path::Path;

const MAX_FILE_BYTES: u64 = 200 * 1024 * 1024;
const MAX_TOTAL_BYTES: u64 = 500 * 1024 * 1024;
// Defensive traversal bounds, not upstream protocol limits.
const MAX_ENTRIES: usize = 4096;
const MAX_DEPTH: usize = 64;

#[derive(Default)]
struct Budget {
    entries: usize,
    bytes: u64,
}

fn invalid() -> io::Error {
    io::ErrorKind::InvalidData.into()
}

pub(crate) fn outputs(root: &Path, output: impl Write) -> io::Result<()> {
    let root = workspace_path::anchor(root)?;
    let mut archive = tar::Builder::new(output);
    let selected = match workspace_path::directory(&root, "outputs") {
        Ok(fd) => fd,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return archive.finish(),
        Err(error) => return Err(error),
    };
    let device = fstat(&root)?.st_dev;
    walk(
        &selected,
        "outputs",
        device,
        0,
        &mut Budget::default(),
        &mut archive,
    )?;
    archive.finish()
}

fn walk<W: Write>(
    parent: &OwnedFd,
    path: &str,
    device: u64,
    depth: usize,
    budget: &mut Budget,
    archive: &mut tar::Builder<W>,
) -> io::Result<()> {
    if depth > MAX_DEPTH || path.len() > 4096 {
        return Err(invalid());
    }
    let before = fstat(parent)?;
    if before.st_dev != device {
        return Err(invalid());
    }
    let mut listing = observe(parent, MAX_ENTRIES)?;
    budget.entries += listing.visited;
    if listing.truncated || budget.entries > MAX_ENTRIES {
        return Err(invalid());
    }
    listing.entries.sort_by(|a, b| a.name.cmp(&b.name));
    for entry in &listing.entries {
        let path = format!("{path}/{}", entry.name);
        if path.len() > 4096 {
            return Err(invalid());
        }
        match entry.kind {
            FileType::Directory => {
                let child = workspace_path::directory(parent, &entry.name)?;
                walk(&child, &path, device, depth + 1, budget, archive)?;
            }
            FileType::RegularFile => {
                let file = openat(
                    parent,
                    entry.name.as_str(),
                    OFlags::RDONLY | OFlags::NOFOLLOW | OFlags::NONBLOCK | OFlags::CLOEXEC,
                    Mode::empty(),
                )?;
                append(File::from(file), &path, device, budget, archive)?;
            }
            _ => return Err(invalid()),
        }
    }
    // Directory timestamps may have coarser resolution than successive mutations.
    let mut after = observe(parent, MAX_ENTRIES)?;
    after.entries.sort_by(|a, b| a.name.cmp(&b.name));
    if after.truncated || after.entries != listing.entries || !unchanged(&before, &fstat(parent)?) {
        return Err(invalid());
    }
    Ok(())
}

fn append<W: Write>(
    mut file: File,
    path: &str,
    device: u64,
    budget: &mut Budget,
    archive: &mut tar::Builder<W>,
) -> io::Result<()> {
    let before = fstat(&file)?;
    if FileType::from_raw_mode(before.st_mode) != FileType::RegularFile
        || before.st_dev != device
        || before.st_nlink != 1
    {
        return Err(invalid());
    }
    let size = u64::try_from(before.st_size).map_err(|_| invalid())?;
    if size > MAX_FILE_BYTES || size > MAX_TOTAL_BYTES - budget.bytes {
        return Err(invalid());
    }
    budget.bytes += size;
    let mut header = tar::Header::new_gnu();
    header.set_entry_type(tar::EntryType::Regular);
    header.set_mode(0o600);
    header.set_size(size);
    // Read through the held descriptor, never reopen an attacker-controlled path.
    let mut body = (&mut file).take(size);
    archive.append_data(&mut header, path, &mut body)?;
    if body.limit() != 0 || !unchanged(&before, &fstat(&file)?) {
        return Err(invalid());
    }
    Ok(())
}

fn unchanged(a: &Stat, b: &Stat) -> bool {
    a.st_dev == b.st_dev
        && a.st_ino == b.st_ino
        && a.st_mode == b.st_mode
        && a.st_nlink == b.st_nlink
        && a.st_size == b.st_size
        && a.st_mtime == b.st_mtime
        && a.st_mtime_nsec == b.st_mtime_nsec
        && a.st_ctime == b.st_ctime
        && a.st_ctime_nsec == b.st_ctime_nsec
}

#[cfg(test)]
#[path = "export_tests.rs"]
mod tests;
