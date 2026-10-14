use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Envelope<T = serde_json::Value> {
    #[serde(default)]
    pub version: String,
    #[serde(rename = "type")]
    pub message_type: String,
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub method: String,
    #[serde(default)]
    pub params: Option<T>,
    #[serde(default)]
    pub result: Option<T>,
    #[serde(default)]
    pub error: Option<WireError>,
    #[serde(default)]
    pub stream: Option<String>,
    #[serde(default)]
    pub data: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WireError {
    pub code: String,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Hello {
    pub client: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub client_version: Option<String>,
    #[serde(default)]
    pub capabilities: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Welcome {
    pub protocol: String,
    #[serde(default)]
    pub server_version: Option<String>,
    #[serde(default)]
    pub methods: Vec<String>,
    #[serde(default)]
    pub capabilities: Vec<String>,
}
