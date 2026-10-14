use crate::{
    models::{AgentList, Pane, Process, Tab, TerminalSnapshot, WorkspaceSnapshot},
    protocol::Connection,
    views::ExtensionContribution,
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
    pub fn extend(
        &mut self,
        plugin_id: &str,
        contribution: ExtensionContribution,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        if !self
            .connection
            .welcome
            .capabilities
            .iter()
            .any(|capability| capability == "plugin.contributions")
        {
            return Err("Hive daemon does not advertise plugin.contributions".into());
        }
        let _: serde_json::Value = self.connection.call(
            "plugin.contribute",
            json!({"plugin": plugin_id, "contribution": contribution}),
        )?;
        Ok(())
    }
}
