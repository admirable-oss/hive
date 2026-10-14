//! Official Rust SDK for Hive plugin processes.
//!
//! The crate reads the host invocation context, negotiates protocol 2, and
//! exposes typed services while keeping transport and model code separate.
pub mod models;
pub mod protocol;
pub mod services;
pub mod views;

pub use models::{Agent, AgentList, Event, PluginContext, Process, Tab, WorkspaceSnapshot};
pub use protocol::{Connection, HiveProtocolError, Welcome};
pub use services::Hive;
pub use views::{
    CommandContribution, ExtensionContribution, KeybindingContribution, ViewContribution,
};

use std::{env, io, path::PathBuf};

/// An initialized plugin invocation and its Hive daemon client.
pub struct Plugin {
    pub context: PluginContext,
    pub hive: Hive,
}

impl Plugin {
    /// Read invocation JSON from stdin and connect using HIVE_SOCKET_PATH.
    pub fn init() -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        Self::from_reader(io::stdin().lock())
    }

    /// Initialize from a supplied context reader (useful for tests and hosts).
    pub fn from_reader(
        reader: impl io::Read,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        let context = PluginContext::read(reader)?;
        let socket = env::var_os("HIVE_SOCKET_PATH")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                env::var_os("HOME")
                    .map(PathBuf::from)
                    .unwrap_or_default()
                    .join(".hive/hive.sock")
            });
        let hive = Hive::connect(&socket, &context.plugin, env!("CARGO_PKG_VERSION"))?;
        Ok(Self { context, hive })
    }
}
