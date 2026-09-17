use super::*;
use std::fs;
use std::io::Cursor;
use std::os::unix::fs::{MetadataExt, PermissionsExt, symlink};

fn fixture() -> tempfile::TempDir {
    let root = std::path::PathBuf::from(std::env::var_os("HOME").expect("HOME required"))
        .join(".parsar/tests/scoped-write");
    fs::create_dir_all(&root).unwrap();
    tempfile::tempdir_in(root).unwrap()
}

fn frame(data: &[u8]) -> impl Read + '_ {
    Cursor::new(data).chain(Cursor::new(Sha256::digest(data).to_vec()))
}

fn install(root: &Path, relative: &str, size: u64, input: impl Read) -> Result<(), Failure> {
    let staging = fixture();
    let result = super::install(root, relative, size, input, staging.path());
    assert_eq!(fs::read_dir(staging.path()).unwrap().count(), 0);
    result
}

#[test]
fn requires_existing_disjoint_nofollow_staging_before_consuming_input() {
    let f = fixture();
    let root = f.path().join("workspace");
    let staging = f.path().join("private");
    fs::create_dir_all(root.join("nested")).unwrap();
    fs::create_dir(&staging).unwrap();
    fs::write(root.join("file"), b"old").unwrap();
    symlink(&staging, f.path().join("link")).unwrap();
    struct Unread;
    impl Read for Unread {
        fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
            panic!("invalid staging must be rejected before reading input")
        }
    }
    for invalid in [
        root.clone(),
        root.join("nested"),
        f.path().to_path_buf(),
        f.path().join("missing"),
        f.path().join("link"),
        staging.join("../private"),
        Path::new("relative").to_path_buf(),
    ] {
        let failure = super::install(&root, "file", 3, Unread, &invalid).unwrap_err();
        assert!(!failure.committed);
        assert_eq!(fs::read(root.join("file")).unwrap(), b"old");
    }
    assert_eq!(fs::read_dir(&staging).unwrap().count(), 0);
}

#[test]
fn replaces_only_destination_hard_link_and_keeps_exact_binary_bytes() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir(&root).unwrap();
    let outside = f.path().join("outside");
    fs::write(&outside, b"outside").unwrap();
    fs::hard_link(&outside, root.join("file")).unwrap();
    let input: Vec<_> = (0..=255).cycle().take(256 * 1024).collect();
    install(&root, "file", input.len() as u64, frame(&input)).unwrap();
    assert_eq!(fs::read(&outside).unwrap(), b"outside");
    assert_eq!(fs::read(root.join("file")).unwrap(), input);
    assert_ne!(
        fs::metadata(&outside).unwrap().ino(),
        fs::metadata(root.join("file")).unwrap().ino()
    );
    assert_eq!(
        fs::metadata(root.join("file"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    install(&root, "file", 0, frame(b"")).unwrap();
    assert!(fs::read(root.join("file")).unwrap().is_empty());
}

#[test]
fn incomplete_excess_and_failed_input_preserve_old_file_and_remove_staging() {
    let f = fixture();
    fs::write(f.path().join("file"), b"old").unwrap();
    for (size, data) in [(4, b"new".as_slice()), (2, b"new".as_slice())] {
        assert!(
            !install(f.path(), "file", size, Cursor::new(data))
                .unwrap_err()
                .committed
        );
        assert_eq!(fs::read(f.path().join("file")).unwrap(), b"old");
        assert_eq!(fs::read_dir(f.path()).unwrap().count(), 1);
    }
    struct Failing;
    impl Read for Failing {
        fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
            Err(io::ErrorKind::BrokenPipe.into())
        }
    }
    assert!(!install(f.path(), "file", 3, Failing).unwrap_err().committed);
    assert_eq!(fs::read(f.path().join("file")).unwrap(), b"old");
    assert_eq!(fs::read_dir(f.path()).unwrap().count(), 1);
}

#[test]
fn rejects_symlinks_traversal_nonregular_targets_and_oversized_inputs() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir(&root).unwrap();
    fs::create_dir(f.path().join("outside")).unwrap();
    fs::write(f.path().join("outside/file"), b"canary").unwrap();
    symlink(f.path().join("outside/file"), root.join("link")).unwrap();
    symlink(f.path().join("outside"), root.join("parent")).unwrap();
    for name in [
        "link",
        "parent/file",
        "../outside/file",
        "/file",
        "a/../file",
        "a//file",
        "",
        ".",
        "a\\b",
        "x\n",
        "missing/file",
    ] {
        assert!(
            install(&root, name, 3, Cursor::new(b"new")).is_err(),
            "{name:?}"
        );
    }
    assert!(install(&root, "parent", 3, Cursor::new(b"new")).is_err());
    fs::create_dir(root.join("directory")).unwrap();
    assert!(install(&root, "directory", 0, io::empty()).is_err());
    assert!(install(&root, "large", MAX_BYTES + 1, io::empty()).is_err());
    assert_eq!(fs::read(f.path().join("outside/file")).unwrap(), b"canary");
}

#[test]
fn supports_large_stream_without_whole_input_allocation() {
    let f = fixture();
    let mut digest = Sha256::new();
    for _ in 0..(MAX_BYTES / 8192) {
        digest.update([0x91; 8192]);
    }
    let input = io::repeat(0x91)
        .take(MAX_BYTES)
        .chain(Cursor::new(digest.finalize().to_vec()));
    install(f.path(), "large", MAX_BYTES, input).unwrap();
    let file = fs::File::open(f.path().join("large")).unwrap();
    assert_eq!(file.metadata().unwrap().len(), MAX_BYTES);
    let mut content = io::BufReader::new(file);
    let mut chunk = [0u8; 8192];
    loop {
        let n = content.read(&mut chunk).unwrap();
        if n == 0 {
            break;
        }
        assert!(chunk[..n].iter().all(|x| *x == 0x91));
    }
}

#[test]
fn ancestor_replacement_during_input_cannot_redirect_commit() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir_all(root.join("a")).unwrap();
    fs::create_dir(f.path().join("outside")).unwrap();
    fs::write(f.path().join("outside/file"), b"outside").unwrap();
    let mut changed = false;
    let mut bytes = Cursor::new(b"inside");
    let input = std::io::Read::by_ref(&mut bytes);
    struct Replace<'a> {
        read: &'a mut Cursor<&'static [u8; 6]>,
        change: Box<dyn FnMut() + 'a>,
    }
    impl Read for Replace<'_> {
        fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
            (self.change)();
            self.read.read(buf)
        }
    }
    let input = Replace {
        read: input,
        change: Box::new(|| {
            if !changed {
                fs::rename(root.join("a"), root.join("held")).unwrap();
                symlink(f.path().join("outside"), root.join("a")).unwrap();
                changed = true;
            }
        }),
    };
    install(
        &root,
        "a/file",
        6,
        input.chain(Cursor::new(Sha256::digest(b"inside").to_vec())),
    )
    .unwrap();
    assert_eq!(fs::read(root.join("held/file")).unwrap(), b"inside");
    assert_eq!(fs::read(f.path().join("outside/file")).unwrap(), b"outside");
}

#[test]
fn requires_matching_commit_trailer_and_does_not_wait_for_eof() {
    let f = fixture();
    fs::write(f.path().join("file"), b"old").unwrap();
    for data in [b"new".to_vec(), [b"new".as_slice(), &[0u8; 32]].concat()] {
        assert!(install(f.path(), "file", 3, Cursor::new(data)).is_err());
        assert_eq!(fs::read(f.path().join("file")).unwrap(), b"old");
    }
    struct NoMore;
    impl Read for NoMore {
        fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
            panic!("must stop after commit trailer")
        }
    }
    install(f.path(), "file", 3, frame(b"new").chain(NoMore)).unwrap();
    assert_eq!(fs::read(f.path().join("file")).unwrap(), b"new");
}
