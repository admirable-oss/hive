use serde::{Deserialize, Serialize};
use std::io::Read;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Event {
    #[serde(rename = "type")]
    pub event_type: String,
    #[serde(default)]
    pub data: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct PluginContext {
    pub plugin: String,
    pub trigger: String,
    #[serde(default)]
    pub action: Option<String>,
    #[serde(default)]
    pub event: Option<Event>,
    pub session: String,
    #[serde(default)]
    pub environment: Option<String>,
    #[serde(default)]
    pub tab: Option<String>,
    #[serde(default)]
    pub pane: Option<String>,
}

impl PluginContext {
    pub fn read(reader: impl Read) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        let mut bytes = Vec::new();
        reader.take((1 << 20) + 1).read_to_end(&mut bytes)?;
        if bytes.len() > 1 << 20 {
            return Err("Hive plugin context exceeds 1 MiB".into());
        }
        let context: Self = serde_json::from_slice(&bytes)?;
        if context.plugin.is_empty() || context.trigger.is_empty() || context.session.is_empty() {
            return Err(
                "plugin, trigger, and session are required in Hive invocation context".into(),
            );
        }
        Ok(context)
    }
}
