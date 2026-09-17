use rustix::fs::{AtFlags, Dir, FileType, statat};
use std::io;
use std::os::fd::OwnedFd;

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
    io::ErrorKind::InvalidInput.into()
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
