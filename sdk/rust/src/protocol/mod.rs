mod connection;
mod message;

pub use connection::Connection;
pub use message::{Envelope, Hello, Welcome};

/// A typed error returned by the Hive daemon.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HiveProtocolError {
    pub code: String,
    pub message: String,
    pub method: Option<String>,
}

impl std::fmt::Display for HiveProtocolError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.message)
    }
}
impl std::error::Error for HiveProtocolError {}
