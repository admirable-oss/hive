use crate::{
    models::{AgentList, Pane, Process, Tab, TerminalSnapshot, WorkspaceSnapshot},
    protocol::Connection,
    views::{Card, Status},
};
use serde::Serialize;
use serde_json::json;
use std::{error::Error, path::Path};

/// Typed Hive daemon services shared across the language SDKs.
pub struct Hive {
    connection: Connection,
}

#[derive(Debug, Clone, Serialize)]
pub struct TabRename {
    pub id: String,
    pub name: String,
}
#[derive(Debug, Clone, Serialize)]
pub struct PopupRequest {
    pub tab_id: String,
    pub spec: PopupSpec,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub width_pct: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub height_pct: Option<u8>,
}
#[derive(Debug, Clone, Serialize)]
pub struct PopupSpec {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub command: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cwd: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub env: Option<std::collections::HashMap<String, String>>,
}

impl Hive {
    pub fn connect(
        path: impl AsRef<Path>,
        plugin: &str,
        version: &str,
    ) -> Result<Self, Box<dyn Error + Send + Sync>> {
        Ok(Self {
            connection: Connection::connect(path, plugin, version)?,
        })
    }
    pub fn from_connection(connection: Connection) -> Self {
        Self { connection }
    }
    pub fn welcome(&self) -> &crate::protocol::Welcome {
        &self.connection.welcome
    }
    pub fn call<P: Serialize, R: serde::de::DeserializeOwned>(
        &mut self,
        method: &str,
        params: P,
    ) -> Result<R, Box<dyn Error + Send + Sync>> {
        self.connection.call(method, params)
    }
    pub fn tabs(
        &mut self,
        environment_id: Option<&str>,
    ) -> Result<Vec<Tab>, Box<dyn Error + Send + Sync>> {
        self.connection.call(
            "tab.list",
            json!({"environment_id": environment_id.unwrap_or("")}),
        )
    }
    pub fn rename_tab(
        &mut self,
        id: &str,
        name: &str,
    ) -> Result<Tab, Box<dyn Error + Send + Sync>> {
        self.connection.call(
            "tab.rename",
            TabRename {
                id: id.into(),
                name: name.into(),
            },
        )
    }
    pub fn popup(&mut self, request: PopupRequest) -> Result<Pane, Box<dyn Error + Send + Sync>> {
        self.connection.call("pane.popup", request)
    }
    pub fn processes(
        &mut self,
        environment_id: Option<&str>,
    ) -> Result<Vec<Process>, Box<dyn Error + Send + Sync>> {
        self.connection.call(
            "process.list",
            json!({"environment_id": environment_id.unwrap_or("")}),
        )
    }
    pub fn agents(&mut self, all: bool) -> Result<AgentList, Box<dyn Error + Send + Sync>> {
        self.connection.call("agent.list", json!({"all": all}))
    }
    pub fn workspace_snapshot(
        &mut self,
    ) -> Result<WorkspaceSnapshot, Box<dyn Error + Send + Sync>> {
        self.connection.call("api.snapshot", json!({}))
    }
    pub fn terminal_snapshot(
        &mut self,
        process_id: &str,
    ) -> Result<TerminalSnapshot, Box<dyn Error + Send + Sync>> {
        self.connection
            .call("terminal.snapshot", json!({"process_id": process_id}))
    }
    /// Show (or clear) one of the cards the manifest declares.
    pub fn set_card(&mut self, card: &Card) -> Result<(), Box<dyn Error + Send + Sync>> {
        let _: serde_json::Value = self.connection.call("plugin.card", card)?;
        Ok(())
    }
    /// Show (or clear) one of the status indicators the manifest declares.
    pub fn set_status(&mut self, status: &Status) -> Result<(), Box<dyn Error + Send + Sync>> {
        let _: serde_json::Value = self.connection.call("plugin.status", status)?;
        Ok(())
    }
    /// Say startup is done (otherwise 2s of uptime counts).
    pub fn ready(&mut self) -> Result<(), Box<dyn Error + Send + Sync>> {
        let _: serde_json::Value = self.connection.call("plugin.ready", json!({}))?;
        Ok(())
    }
    /// Ask the user for an optional capability; the user grants it.
    pub fn request_capability(
        &mut self,
        capability: &str,
        reason: &str,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        let _: serde_json::Value = self.connection.call(
            "plugin.request",
            json!({"capability": capability, "reason": reason}),
        )?;
        Ok(())
    }
    /// What this hive offers, and what this plugin may call.
    pub fn capabilities(&mut self) -> Result<serde_json::Value, Box<dyn Error + Send + Sync>> {
        self.connection.call("plugin.capabilities", json!({}))
    }
}
