//! Qualification-only scheduling gate inside an existing native blocking write.
#![allow(clippy::expect_used)]

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration;

static ARMED: Mutex<Option<Arc<Gate>>> = Mutex::new(None);

pub(crate) struct Gate {
    path: PathBuf,
    pub(crate) entered: AtomicBool,
    pub(crate) finished: AtomicBool,
    released: Mutex<bool>,
    changed: Condvar,
}

pub(crate) struct ReleaseOnDrop(pub(crate) Arc<Gate>);

impl Gate {
    pub(crate) fn arm(path: PathBuf) -> ReleaseOnDrop {
        let gate = Arc::new(Self {
            path,
            entered: AtomicBool::new(false),
            finished: AtomicBool::new(false),
            released: Mutex::new(false),
            changed: Condvar::new(),
        });
        let mut armed = ARMED.lock().expect("gate registry");
        assert!(armed.is_none(), "only one qualification write at a time");
        *armed = Some(Arc::clone(&gate));
        ReleaseOnDrop(gate)
    }

    pub(crate) fn release(&self) {
        *self.released.lock().expect("gate release") = true;
        self.changed.notify_all();
    }
}

impl Drop for ReleaseOnDrop {
    fn drop(&mut self) {
        self.0.release();
        let mut armed = ARMED.lock().expect("gate registry");
        if armed
            .as_ref()
            .is_some_and(|gate| Arc::ptr_eq(gate, &self.0))
        {
            *armed = None;
        }
    }
}

pub(crate) struct Completion(Arc<Gate>);

impl Drop for Completion {
    fn drop(&mut self) {
        self.0.finished.store(true, Ordering::SeqCst);
    }
}

pub(crate) fn before_write(path: &Path) -> std::io::Result<Option<Completion>> {
    let gate = {
        let mut armed = ARMED.lock().expect("gate registry");
        if armed.as_ref().is_none_or(|gate| gate.path != path) {
            return Ok(None);
        }
        armed.take().expect("matched gate")
    };
    gate.entered.store(true, Ordering::SeqCst);
    let (released, timeout) = gate
        .changed
        .wait_timeout_while(
            gate.released.lock().expect("gate release"),
            Duration::from_secs(15),
            |released| !*released,
        )
        .expect("gate wait");
    if timeout.timed_out() && !*released {
        return Err(std::io::Error::new(
            std::io::ErrorKind::TimedOut,
            "qualification gate was not released",
        ));
    }
    drop(released);
    Ok(Some(Completion(gate)))
}
