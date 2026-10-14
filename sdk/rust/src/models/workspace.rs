use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Tab {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub environment_id: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Pane {
    pub id: String,
    pub tab_id: String,
    #[serde(default)]
    pub process_id: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Process {
    pub id: String,
    #[serde(default)]
    pub command: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub status: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Agent {
    pub id: String,
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub pane_id: Option<String>,
    #[serde(default)]
    pub tab_id: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct AgentList {
    #[serde(default)]
    pub agents: Vec<Agent>,
    #[serde(default)]
    pub rollups: Option<serde_json::Value>,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct WorkspaceSnapshot {
    #[serde(default)]
    pub daemon: Option<serde_json::Value>,
    #[serde(default)]
    pub environments: Vec<serde_json::Value>,
    #[serde(default)]
    pub tabs: Vec<Tab>,
    #[serde(default)]
    pub panes: Vec<Pane>,
    #[serde(default)]
    pub processes: Vec<Process>,
    #[serde(default)]
    pub agents: Vec<Agent>,
    #[serde(default)]
    pub rollups: Option<serde_json::Value>,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct TerminalSnapshot {
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub lines: Vec<String>,
    #[serde(default)]
    pub width: usize,
    #[serde(default)]
    pub height: usize,
}
