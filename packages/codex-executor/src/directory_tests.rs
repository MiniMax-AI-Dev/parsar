use super::*;
use crate::workspace_path::{anchor, directory};
use std::fs;
use std::os::unix::fs::symlink;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};

static TEST_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

struct Fixture {
    path: PathBuf,
    _guard: std::sync::MutexGuard<'static, ()>,
}
impl Fixture {
    fn new() -> Self {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        let guard = TEST_LOCK.lock().unwrap();
        let root = PathBuf::from(std::env::var_os("HOME").expect("HOME required"))
            .join(".parsar/tests/scoped-directory");
        fs::create_dir_all(&root).unwrap();
        let path = root.join(format!(
            "fixture-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        fs::create_dir(&path).unwrap();
        Self {
            path,
            _guard: guard,
        }
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.path).unwrap();
    }
}

#[test]
fn ancestor_replacement_after_open_stays_on_authorized_directory() {
    let f = Fixture::new();
    fs::create_dir_all(f.path.join("workspace/a/sub")).unwrap();
    fs::create_dir_all(f.path.join("outside/sub")).unwrap();
    fs::write(f.path.join("workspace/a/sub/inside"), b"inside").unwrap();
    fs::write(f.path.join("workspace/a/sub/outside-secret"), b"decoy").unwrap();
    fs::write(f.path.join("outside/sub/outside-secret"), b"outside-secret").unwrap();
    let root = anchor(&f.path.join("workspace")).unwrap();
    let selected = directory(&root, "a/sub").unwrap();
    fs::rename(f.path.join("workspace/a"), f.path.join("workspace/held")).unwrap();
    symlink(f.path.join("outside"), f.path.join("workspace/a")).unwrap();
    let path_names: Vec<_> = fs::read_dir(f.path.join("workspace/a/sub"))
        .unwrap()
        .map(|entry| entry.unwrap().file_name())
        .collect();
    assert_eq!(path_names, ["outside-secret"]);
    let mut safe = observe(&selected, 16).unwrap();
    safe.entries.sort_by(|a, b| a.name.cmp(&b.name));
    assert_eq!(
        safe.entries,
        [
            Entry {
                name: "inside".into(),
                kind: FileType::RegularFile,
                size: Some(6)
            },
            Entry {
                name: "outside-secret".into(),
                kind: FileType::RegularFile,
                size: Some(5)
            },
        ]
    );
    assert!(!safe.truncated);
    assert!(directory(&root, "a/sub").is_err());
    fs::remove_file(f.path.join("workspace/a")).unwrap();
    fs::rename(f.path.join("workspace/held"), f.path.join("workspace/a")).unwrap();
    for name in path_names {
        assert!(
            fs::symlink_metadata(f.path.join("workspace/a/sub").join(name))
                .unwrap()
                .is_file()
        );
    }
}

#[test]
fn ancestor_replacement_between_component_opens_stays_on_original_fd() {
    let f = Fixture::new();
    fs::create_dir_all(f.path.join("workspace/a/sub")).unwrap();
    fs::create_dir_all(f.path.join("outside/sub")).unwrap();
    fs::write(f.path.join("workspace/a/sub/inside"), b"inside").unwrap();
    fs::write(f.path.join("outside/sub/outside"), b"outside").unwrap();
    let root = anchor(&f.path.join("workspace")).unwrap();
    let parent = directory(&root, "a").unwrap();
    fs::rename(f.path.join("workspace/a"), f.path.join("workspace/held")).unwrap();
    symlink(f.path.join("outside"), f.path.join("workspace/a")).unwrap();
    let selected = directory(&parent, "sub").unwrap();
    assert_eq!(observe(&selected, 1).unwrap().entries[0].name, "inside");
}

#[test]
fn bound_applies_before_collecting_directory_names() {
    let f = Fixture::new();
    for i in 0..5000 {
        fs::write(f.path.join(format!("entry-{i:05}")), b"x").unwrap();
    }
    let root = anchor(&f.path).unwrap();
    let selected = directory(&root, "").unwrap();
    let result = observe(&selected, 3).unwrap();
    assert_eq!(result.entries.len(), 3);
    assert_eq!(result.visited, 4);
    assert!(result.truncated);
}

#[test]
fn symlink_metadata_does_not_follow_the_target_and_invalid_paths_fail() {
    let f = Fixture::new();
    fs::create_dir(f.path.join("workspace")).unwrap();
    fs::write(
        f.path.join("outside"),
        b"must not become the returned file size",
    )
    .unwrap();
    symlink(f.path.join("outside"), f.path.join("workspace/link")).unwrap();
    let root = anchor(&f.path.join("workspace")).unwrap();
    let selected = directory(&root, "").unwrap();
    let result = observe(&selected, 1).unwrap();
    assert_eq!(
        result.entries,
        [Entry {
            name: "link".into(),
            kind: FileType::Symlink,
            size: None
        }]
    );
    assert!(!result.truncated);
    for path in ["/outside", "..", "a/../b", ".", "a//b", "a/", "a\\b", "a\0"] {
        assert!(directory(&root, path).is_err(), "{path:?}");
    }
    assert!(observe(&selected, 0).is_err());
    assert!(observe(&selected, 4097).is_err());
}

#[test]
fn repeated_reads_release_directory_descriptors() {
    let f = Fixture::new();
    let count = || fs::read_dir("/proc/self/fd").unwrap().count();
    let before = count();
    for _ in 0..200 {
        let root = anchor(&f.path).unwrap();
        let selected = directory(&root, "").unwrap();
        let result = observe(&selected, 1).unwrap();
        assert!(result.entries.is_empty() && !result.truncated);
    }
    assert_eq!(count(), before);
}
