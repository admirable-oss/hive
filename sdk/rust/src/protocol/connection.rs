use super::{Envelope, Hello, HiveProtocolError, Welcome};
use serde::{Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Read, Write},
    os::unix::net::UnixStream,
    path::Path,
};

const MAX_MESSAGE_BYTES: usize = 1 << 20;

/// Synchronous protocol-2 client. Calls are ordered on this connection.
pub struct Connection {
    reader: BufReader<UnixStream>,
    writer: UnixStream,
    sequence: u64,
    pub welcome: Welcome,
}

impl Connection {
    pub fn connect(
        path: impl AsRef<Path>,
        plugin: &str,
        version: &str,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        Self::from_stream(UnixStream::connect(path)?, plugin, version)
    }

    /// Perform the hello/welcome exchange over an already-connected socket.
    pub fn from_stream(
        stream: UnixStream,
        plugin: &str,
        version: &str,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        let mut writer = stream.try_clone()?;
        let mut reader = BufReader::new(stream);
        let hello = Envelope {
            version: "2".into(),
            message_type: "hello".into(),
            id: "hello".into(),
            method: String::new(),
            params: Some(json!(Hello {
                client: format!("hive-plugin:{plugin}"),
                client_version: Some(version.into()),
                capabilities: vec![]
            })),
            result: None,
            error: None,
            stream: None,
            data: None,
        };
        write_frame(&mut writer, &hello)?;
        let response: Envelope = read_frame(&mut reader)?;
        if response.message_type != "welcome" {
            return Err(format!("expected protocol welcome, got {}", response.message_type).into());
        }
        let welcome: Welcome = serde_json::from_value(response.result.unwrap_or(Value::Null))?;
        if welcome.protocol != "2" {
            return Err(format!("unsupported Hive protocol {}", welcome.protocol).into());
        }
        Ok(Self {
            reader,
            writer,
            sequence: 0,
            welcome,
        })
    }

    pub fn call<P: Serialize, R: DeserializeOwned>(
        &mut self,
        method: &str,
        params: P,
    ) -> Result<R, Box<dyn std::error::Error + Send + Sync>> {
        self.sequence += 1;
        let id = self.sequence.to_string();
        let request = Envelope {
            version: "2".into(),
            message_type: "request".into(),
            id: id.clone(),
            method: method.into(),
            params: Some(serde_json::to_value(params)?),
            result: None,
            error: None,
            stream: None,
            data: None,
        };
        write_frame(&mut self.writer, &request)?;
        loop {
            let response: Envelope = read_frame(&mut self.reader)?;
            if response.message_type != "response" || response.id != id {
                continue;
            }
            if let Some(error) = response.error {
                return Err(Box::new(HiveProtocolError {
                    code: error.code,
                    message: error.message,
                    method: Some(method.into()),
                }));
            }
            return Ok(serde_json::from_value(
                response.result.unwrap_or(Value::Null),
            )?);
        }
    }
}

fn write_frame(
    writer: &mut impl Write,
    message: &impl Serialize,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let bytes = serde_json::to_vec(message)?;
    if bytes.len() + 1 > MAX_MESSAGE_BYTES {
        return Err("Hive protocol frame exceeds 1 MiB".into());
    }
    writer.write_all(&bytes)?;
    writer.write_all(b"\n")?;
    writer.flush()?;
    Ok(())
}

fn read_frame(
    reader: &mut impl BufRead,
) -> Result<Envelope, Box<dyn std::error::Error + Send + Sync>> {
    let mut bytes = Vec::new();
    let read = reader
        .take((MAX_MESSAGE_BYTES + 1) as u64)
        .read_until(b'\n', &mut bytes)?;
    if read == 0 {
        return Err("Hive daemon closed the connection".into());
    }
    if bytes.len() > MAX_MESSAGE_BYTES {
        return Err("Hive protocol frame exceeds 1 MiB".into());
    }
    Ok(serde_json::from_slice(&bytes)?)
}
