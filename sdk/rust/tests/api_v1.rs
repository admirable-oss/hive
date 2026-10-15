use hive_plugin_sdk::{Card, CardRow, Link, PluginContext, capability, ttl_nanos};
use std::{fs, path::PathBuf};

fn v1(name: &str) -> String {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../protocol/fixtures/v1")
        .join(name);
    fs::read_to_string(path).expect("fixture")
}

#[test]
fn contexts_from_shared_fixtures() {
    let action: PluginContext = serde_json::from_str(&v1("context-action.json")).unwrap();
    assert_eq!(action.api, 1);
    assert!(action.has(capability::HIVE_WRITE));
    assert!(!action.has(capability::NETWORK));
    assert_eq!(action.action.as_deref(), Some("show"));
    let event: PluginContext = serde_json::from_str(&v1("context-event.json")).unwrap();
    let ev = event.event.unwrap();
    assert_eq!((ev.event_type.as_str(), ev.seq), ("worktree.created", 42));
}

#[test]
fn card_serializes_to_the_wire_shape() {
    let want: serde_json::Value = serde_json::from_str(&v1("request-plugin-card.json")).unwrap();
    let card = Card {
        card: "build".into(),
        environment: "api".into(),
        rows: vec![CardRow {
            label: "main".into(),
            text: "passing".into(),
            tone: "success".into(),
        }],
        links: vec![Link {
            title: "Run".into(),
            url: "https://ci.example.com/run/1".into(),
        }],
        ttl: ttl_nanos(600.0),
        ..Default::default()
    };
    assert_eq!(serde_json::to_value(&card).unwrap(), want["params"]);
}
