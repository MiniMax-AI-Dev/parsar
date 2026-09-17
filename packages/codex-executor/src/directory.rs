use rustix::fs::{AtFlags, Dir, FileType, Mode, OFlags, open, openat, statat};
use std::io;
use std::os::fd::OwnedFd;
use std::path::{Component, Path};

#[derive(Debug, PartialEq)]
pub(crate) struct Entry {
    pub name: String,
    pub kind: FileType,
    pub size: Option<u64>,
}

#[derive(Debug)]
pub(crate) struct Observation {
    pub entries: Vec<Entry>,
    pub truncated: bool,
    pub visited: usize,
}

fn invalid() -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, "invalid directory request")
}

pub(crate) fn anchor(root: &Path) -> io::Result<OwnedFd> {
    if !root.is_absolute() || root.as_os_str().len() > 4096 {
        return Err(invalid());
    }
    let mut fd = open(
        "/",
        OFlags::PATH | OFlags::DIRECTORY | OFlags::CLOEXEC,
        Mode::empty(),
    )?;
    for part in root.components() {
        match part {
            Component::RootDir => (),
            Component::Normal(name) => {
                fd = openat(
                    &fd,
                    name,
                    OFlags::PATH | OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
                    Mode::empty(),
                )?;
            }
            _ => return Err(invalid()),
        }
    }
    Ok(fd)
}

pub(crate) fn directory(root: &OwnedFd, relative: &str) -> io::Result<OwnedFd> {
    if relative.len() > 4096 {
        return Err(invalid());
    }
    let mut fd = openat(
        root,
        ".",
        OFlags::PATH | OFlags::DIRECTORY | OFlags::CLOEXEC,
        Mode::empty(),
    )?;
    if !relative.is_empty() {
        for part in relative.split('/') {
            if part.is_empty()
                || part == "."
                || part == ".."
                || part.contains(['\\', '\0', '\r', '\n'])
            {
                return Err(invalid());
            }
            fd = openat(
                &fd,
                part,
                OFlags::PATH | OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
                Mode::empty(),
            )?;
        }
    }
    Ok(openat(
        &fd,
        ".",
        OFlags::RDONLY | OFlags::DIRECTORY | OFlags::CLOEXEC,
        Mode::empty(),
    )?)
}

pub(crate) fn observe(fd: &OwnedFd, limit: usize) -> io::Result<Observation> {
    if !(1..=4096).contains(&limit) {
        return Err(invalid());
    }
    let mut result = Observation {
        entries: Vec::with_capacity(limit),
        truncated: false,
        visited: 0,
    };
    for entry in Dir::read_from(fd)? {
        let entry = entry?;
        let name = entry.file_name().to_str().map_err(|_| invalid())?;
        if name == "." || name == ".." {
            continue;
        }
        result.visited += 1;
        if result.entries.len() == limit {
            result.truncated = true;
            break;
        }
        if name.len() > 255 || name.contains(['\\', '\0', '\r', '\n']) {
            return Err(invalid());
        }
        let metadata = statat(fd, entry.file_name(), AtFlags::SYMLINK_NOFOLLOW)?;
        let kind = FileType::from_raw_mode(metadata.st_mode);
        let size = if kind == FileType::RegularFile {
            Some(u64::try_from(metadata.st_size).map_err(|_| invalid())?)
        } else {
            None
        };
        result.entries.push(Entry {
            name: name.into(),
            kind,
            size,
        });
    }
    Ok(result)
}

#[cfg(test)]
#[path = "directory_tests.rs"]
mod tests;
