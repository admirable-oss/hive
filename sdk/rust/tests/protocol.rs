use hive_plugin_sdk::{Connection, HiveProtocolError, PluginContext};
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Write},
    os::unix::net::UnixStream,
    thread,
};

#[test]
fn context_matches_host_invocation_contract() {
    let input = r#"{"plugin":"example","trigger":"event","session":"s1","event":{"type":"process.output","data":{"id":"p1"}}}"#;
    let context = PluginContext::read(input.as_bytes()).unwrap();
    assert_eq!(context.plugin, "example");
    assert_eq!(context.event.unwrap().event_type, "process.output");
    assert!(PluginContext::read(br#"{"plugin":"x","trigger":"startup"}"#.as_slice()).is_err());
    assert!(PluginContext::read(vec![b' '; (1 << 20) + 1].as_slice()).is_err());
}

#[test]
fn hello_call_result_and_error_follow_shared_wire_fixtures() {
    let (client, server) = UnixStream::pair().unwrap();
    let server_thread = thread::spawn(move || {
        let mut reader = BufReader::new(server.try_clone().unwrap());
        let mut writer = server;
        let mut frame = String::new();
        reader.read_line(&mut frame).unwrap();
        let hello: Value = serde_json::from_str(&frame).unwrap();
        assert_eq!(hello["type"], "hello");
        let welcome = include_str!("../../protocol/fixtures/welcome.json");
        writeln!(
            writer,
            "{}",
            serde_json::from_str::<Value>(welcome).unwrap()
        )
        .unwrap();

        frame.clear();
        reader.read_line(&mut frame).unwrap();
        let request: Value = serde_json::from_str(&frame).unwrap();
        let fixture: Value = serde_json::from_str(include_str!(
            "../../protocol/fixtures/request-tab-rename.json"
        ))
        .unwrap();
        assert_eq!(request["method"], fixture["method"]);
        assert_eq!(request["params"], fixture["params"]);
        let mut response = serde_json::from_str::<Value>(include_str!(
            "../../protocol/fixtures/response-tab-rename.json"
        ))
        .unwrap();
        response["id"] = request["id"].clone();
        writeln!(writer, "{response}").unwrap();

        frame.clear();
        reader.read_line(&mut frame).unwrap();
        let request: Value = serde_json::from_str(&frame).unwrap();
        let mut error = serde_json::from_str::<Value>(include_str!(
            "../../protocol/fixtures/error-not-found.json"
        ))
        .unwrap();
        error["id"] = request["id"].clone();
        writeln!(writer, "{error}").unwrap();
    });
    let mut connection = Connection::from_stream(client, "example", "1.0.0").unwrap();
    let result: Value = connection
        .call("tab.rename", json!({"id":"tab-1","name":"Build"}))
        .unwrap();
    assert_eq!(result["name"], "Build");
    let error = connection
        .call::<_, Value>("tab.get", json!({"id":"missing"}))
        .unwrap_err();
    let protocol_error = error.downcast_ref::<HiveProtocolError>().unwrap();
    assert_eq!(protocol_error.code, "not_found");
    server_thread.join().unwrap();
}
