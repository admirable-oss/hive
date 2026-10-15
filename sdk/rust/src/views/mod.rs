//! Plugin API 1 UI surfaces.
//!
//! What a plugin adds to Hive's UI is declared in hive-plugin.toml under
//! `[contributes]`; Hive draws it from data. At run time a plugin sets the
//! content of its declared cards and status indicators (`Hive::set_card`,
//! `Hive::set_status`).
use serde::{Deserialize, Serialize};

/// Capabilities a plugin may hold.
pub mod capability {
    pub const HIVE_READ: &str = "hive.read";
    pub const HIVE_WRITE: &str = "hive.write";
    pub const HIVE_INPUT: &str = "hive.input";
    pub const HIVE_PROCESS: &str = "hive.process";
    pub const HIVE_ADMIN: &str = "hive.admin";
    pub const FILESYSTEM_WRITE: &str = "filesystem.write";
    pub const NETWORK: &str = "network";
    pub const PROCESS_EXEC: &str = "process.exec";
}

/// One line of a card. Tone is "", info, success, warning or error.
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct CardRow {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub label: String,
    pub text: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub tone: String,
}

/// An http(s) link on a card.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Link {
    pub title: String,
    pub url: String,
}

/// What one of the plugin's declared cards shows; no rows and no links clear it.
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct Card {
    pub card: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub environment: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub pane: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub rows: Vec<CardRow>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub links: Vec<Link>,
    /// Nanoseconds it shows (`ttl_nanos`); 0 is the default, 30 minutes.
    #[serde(default, skip_serializing_if = "is_zero")]
    pub ttl: u64,
}

/// A declared tab-bar indicator's text; no text clears it.
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct Status {
    pub indicator: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub text: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub tone: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub ttl: u64,
}

fn is_zero(n: &u64) -> bool {
    *n == 0
}

/// Seconds as the wire's nanosecond duration.
pub fn ttl_nanos(seconds: f64) -> u64 {
    (seconds * 1e9).round() as u64
}
