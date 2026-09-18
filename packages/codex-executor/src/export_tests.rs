use super::*;
use std::collections::BTreeMap;
use std::fs;
use std::os::unix::{fs::symlink, net::UnixListener};

#[test]
fn captures_nested_binary_empty_and_long_names_without_other_workspace_files() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    let root = tempfile::tempdir().unwrap();
    fs::create_dir_all(root.path().join("outputs/nested")).unwrap();
    fs::write(root.path().join("private.txt"), "not an output").unwrap();
    fs::write(root.path().join("outputs/empty"), []).unwrap();
    let body: Vec<_> = (0..=255).cycle().take(131_079).collect();
    let name = format!("outputs/nested/{}", "x".repeat(200));
    fs::write(root.path().join(&name), &body).unwrap();
    let mut bytes = Vec::new();
    outputs(root.path(), &mut bytes).unwrap();
    let mut archive = tar::Archive::new(bytes.as_slice());
    let mut actual = BTreeMap::new();
    for entry in archive.entries().unwrap() {
        let mut entry = entry.unwrap();
        assert!(entry.header().entry_type().is_file());
        let name = entry.path().unwrap().to_str().unwrap().to_owned();
        let mut data = Vec::new();
        entry.read_to_end(&mut data).unwrap();
        actual.insert(name, data);
    }
    assert_eq!(
        actual,
        BTreeMap::from([("outputs/empty".into(), vec![]), (name, body)])
    );
}

#[test]
fn absent_outputs_is_an_empty_archive() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    let root = tempfile::tempdir().unwrap();
    let mut bytes = Vec::new();
    outputs(root.path(), &mut bytes).unwrap();
    assert_eq!(
        tar::Archive::new(bytes.as_slice())
            .entries()
            .unwrap()
            .count(),
        0
    );
}

#[test]
fn rejects_links_and_special_files_without_exposing_their_contents() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    for kind in ["root-symlink", "file-symlink", "hardlink", "socket"] {
        let root = tempfile::tempdir().unwrap();
        let other = tempfile::tempdir().unwrap();
        let secret = b"private daemon credential marker";
        fs::write(other.path().join("secret"), secret).unwrap();
        if kind == "root-symlink" {
            symlink(other.path(), root.path().join("outputs")).unwrap();
        } else {
            fs::create_dir(root.path().join("outputs")).unwrap();
        }
        let target = root.path().join("outputs/file");
        let _socket = match kind {
            "file-symlink" => {
                symlink(other.path().join("secret"), target).unwrap();
                None
            }
            "hardlink" => {
                fs::hard_link(other.path().join("secret"), target).unwrap();
                None
            }
            "socket" => Some(UnixListener::bind(target).unwrap()),
            _ => None,
        };
        let mut bytes = Vec::new();
        assert!(outputs(root.path(), &mut bytes).is_err(), "{kind}");
        assert!(
            !bytes.windows(secret.len()).any(|part| part == secret),
            "{kind}"
        );
    }
}

#[test]
fn enforces_file_and_total_bytes_without_loading_bodies() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    for sizes in [
        vec![MAX_FILE_BYTES + 1],
        vec![MAX_FILE_BYTES, MAX_FILE_BYTES, 101 << 20],
    ] {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("outputs")).unwrap();
        for (index, size) in sizes.iter().enumerate() {
            File::create(root.path().join(format!("outputs/{index}")))
                .unwrap()
                .set_len(*size)
                .unwrap();
        }
        assert!(outputs(root.path(), io::sink()).is_err());
    }
}

#[test]
fn rejects_a_changed_file_or_directory_during_export() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    struct Mutate<'a> {
        root: &'a Path,
        directory: bool,
        done: bool,
    }
    impl Write for Mutate<'_> {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            if !self.done {
                self.done = true;
                if self.directory {
                    fs::write(self.root.join("outputs/new"), "late")?;
                } else {
                    fs::write(self.root.join("outputs/file"), "changed-length")?;
                }
            }
            Ok(bytes.len())
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
    for directory in [false, true] {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("outputs")).unwrap();
        fs::write(root.path().join("outputs/file"), "initial").unwrap();
        assert!(
            outputs(
                root.path(),
                Mutate {
                    root: root.path(),
                    directory,
                    done: false
                }
            )
            .is_err(),
            "directory={directory}"
        );
    }
}

#[test]
fn rejects_truncated_traversal_and_broken_destination() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    let root = tempfile::tempdir().unwrap();
    fs::create_dir(root.path().join("outputs")).unwrap();
    for index in 0..=MAX_ENTRIES {
        fs::write(root.path().join(format!("outputs/{index}")), []).unwrap();
    }
    assert!(outputs(root.path(), io::sink()).is_err());
    struct Broken;
    impl Write for Broken {
        fn write(&mut self, _: &[u8]) -> io::Result<usize> {
            Err(io::ErrorKind::BrokenPipe.into())
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
    let empty = tempfile::tempdir().unwrap();
    assert!(outputs(empty.path(), Broken).is_err());
}
