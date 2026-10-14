//! TUI contribution models. Interactive views run inside ordinary Hive panes.
use serde::Serialize;

#[derive(Debug, Clone, Default, Serialize)]
pub struct ExtensionContribution {
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub commands: Vec<CommandContribution>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub views: Vec<ViewContribution>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub keybindings: Vec<KeybindingContribution>,
}
#[derive(Debug, Clone, Serialize)]
pub struct CommandContribution {
    pub id: String,
    pub title: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
}
#[derive(Debug, Clone, Serialize)]
pub struct ViewContribution {
    pub id: String,
    pub title: String,
    pub location: String,
}
#[derive(Debug, Clone, Serialize)]
pub struct KeybindingContribution {
    pub key: String,
    pub command: String,
}
