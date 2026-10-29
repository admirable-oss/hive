use hive_plugin_sdk::{PluginContext, capability};
use std::{fs, path::PathBuf};

fn v2(name: &str) -> String {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../protocol/fixtures/v2")
        .join(name);
    fs::read_to_string(path).expect("fixture")
}

#[test]
fn contexts_and_capabilities() {
    let action: PluginContext = serde_json::from_str(&v2("context-action.json")).unwrap();
    assert_eq!(action.api, 2);
    assert!(action.has(capability::FILESYSTEM_READ_WORKSPACE));
    assert!(action.has(capability::UNCONFINED));
    let discovery: serde_json::Value = serde_json::from_str(&v2("capabilities.json")).unwrap();
    let names: Vec<&str> = discovery["capabilities"]
        .as_array()
        .unwrap()
        .iter()
        .map(|c| c["name"].as_str().unwrap())
        .collect();
    for c in [
        capability::FILESYSTEM_READ,
        capability::FILESYSTEM_READ_WORKSPACE,
        capability::FILESYSTEM_WRITE_WORKSPACE,
        capability::UNCONFINED,
        capability::PROCESS_EXEC,
    ] {
        assert!(names.contains(&c), "{c}");
    }
}
