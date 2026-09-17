use rustix::fs::{Mode, OFlags, open, openat};
use std::io;
use std::os::fd::OwnedFd;
use std::path::{Component, Path};

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
