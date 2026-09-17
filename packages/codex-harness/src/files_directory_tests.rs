use super::*;

#[test]
fn directory_root_and_limits_are_operation_specific() {
    let binding = Binding {
        native_binary: "/native".into(),
        environment: "expected".into(),
        workspace: "/workspace".into(),
        ipc_root: "/unused".into(),
    };
    for path in ["", "nested/path"] {
        let request = json!({"environment_id":"expected", "operation":"list_directory", "path":path, "max_entries":2});
        let command = request_command(format!("{request}\n").as_bytes(), &binding)
            .expect("directory request");
        let (root, limit) = command.directory.expect("directory operation");
        assert_eq!(
            root.to_abs_path().expect("absolute root").as_path(),
            Path::new("/workspace")
        );
        assert_eq!(limit, 2);
        assert!(command.read_limit.is_none());
    }
    for path in ["/outside", ".", "a/../b", "a//b", "a/", "a\r", "a\n"] {
        let request = json!({"environment_id":"expected", "operation":"list_directory", "path":path, "max_entries":2});
        assert!(request_command(format!("{request}\n").as_bytes(), &binding).is_err());
    }
    for fields in [
        json!({"max_entries":null}),
        json!({"max_entries":0}),
        json!({"max_entries":directory::MAX_ENTRIES+1}),
        json!({"max_entries":2,"max_bytes":1}),
    ] {
        let mut request =
            json!({"environment_id":"expected", "operation":"list_directory", "path":""});
        request
            .as_object_mut()
            .expect("object")
            .extend(fields.as_object().expect("fields").clone());
        assert!(request_command(format!("{request}\n").as_bytes(), &binding).is_err());
    }
    assert!(
        request_command(
            b"{\"environment_id\":\"expected\",\"path\":\"\"}\n",
            &binding
        )
        .is_err()
    );
}
