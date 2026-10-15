# Hive plugin SDK for Rust

Crate: `hive-plugin-sdk` (`hive_plugin_sdk`). It uses the shared Hive protocol
2 contract documented in [`../typescript/PROTOCOL.md`](../typescript/PROTOCOL.md)
and the golden envelopes under [`../protocol/fixtures`](../protocol/fixtures).

```rust
use hive_plugin_sdk::Plugin;

fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let mut plugin = Plugin::init()?;
    let snapshot = plugin.hive.workspace_snapshot()?;
    println!("{} tabs", snapshot.tabs.len());
    Ok(())
}
```

The modules separate `models`, `protocol`, `services`, and `views`. Use the
typed methods for common operations and `Hive::call` for methods described by
the daemon's `api.schema`. UI contributions are declared in the manifest's
`[contributes]` (plugin API 1); `Hive::set_card`, `set_status`, `ready`,
`request_capability` and `capabilities` cover the plugin's run-time side.

Run the crate tests with `cargo test`.
