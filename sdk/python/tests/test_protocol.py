import json
import threading
import unittest
from io import StringIO
from pathlib import Path
from socket import socketpair

from hive_plugin_sdk import Connection, HiveProtocolError, PluginContext

FIXTURES = Path(__file__).resolve().parents[2] / "protocol" / "fixtures"


class ProtocolTests(unittest.TestCase):
    def test_context_matches_plugin_host_contract(self) -> None:
        context = PluginContext.from_dict({
            "plugin": "example", "trigger": "event", "session": "s1",
            "event": {"type": "process.output", "data": {"id": "p1"}},
        })
        self.assertEqual(context.plugin, "example")
        self.assertEqual(context.event.type, "process.output")
        with self.assertRaises(ValueError):
            PluginContext.from_dict({"plugin": "example", "trigger": "startup"})
        with self.assertRaises(ValueError):
            from hive_plugin_sdk import init
            init(stream=StringIO(" " * ((1 << 20) + 1)))

    def test_protocol_handshake_call_and_structured_error_use_shared_fixtures(self) -> None:
        client_socket, server_socket = socketpair()
        failure: list[BaseException] = []

        def serve() -> None:
            try:
                reader = server_socket.makefile("rb")
                hello = json.loads(reader.readline())
                self.assertEqual(hello["type"], "hello")
                server_socket.sendall((FIXTURES / "welcome.json").read_bytes())

                request = json.loads(reader.readline())
                expected = json.loads((FIXTURES / "request-tab-rename.json").read_text())
                self.assertEqual(request["method"], expected["method"])
                self.assertEqual(request["params"], expected["params"])
                response = json.loads((FIXTURES / "response-tab-rename.json").read_text())
                response["id"] = request["id"]
                server_socket.sendall(json.dumps(response).encode() + b"\n")

                request = json.loads(reader.readline())
                response = json.loads((FIXTURES / "error-not-found.json").read_text())
                response["id"] = request["id"]
                server_socket.sendall(json.dumps(response).encode() + b"\n")
            except BaseException as error:  # propagate fixture-thread assertions
                failure.append(error)
            finally:
                server_socket.close()

        thread = threading.Thread(target=serve)
        thread.start()
        connection = Connection(client_socket, "example", "1.0.0")
        result = connection.call("tab.rename", {"id": "tab-1", "name": "Build"})
        self.assertEqual(result, {"id": "tab-1", "name": "Build"})
        with self.assertRaises(HiveProtocolError) as caught:
            connection.call("tab.get", {"id": "missing"})
        self.assertEqual(caught.exception.code, "not_found")
        self.assertEqual(caught.exception.method, "tab.get")
        connection.close()
        thread.join(timeout=2)
        self.assertFalse(thread.is_alive())
        if failure:
            raise failure[0]


if __name__ == "__main__":
    unittest.main()
