# Hive plugin SDK for Python

PyPI distribution: `hive-plugin-sdk`; import package: `hive_plugin_sdk`.
Python 3.10+; runtime dependencies are Python's standard library only.

```python
from hive_plugin_sdk import init

plugin = init()
try:
    snapshot = plugin.hive.workspace_snapshot()
    print(f"{len(snapshot.tabs)} tabs")
finally:
    plugin.close()
```

The package is organized into `models`, `protocol`, `services`, and `views`.
See the shared protocol guide in [`../typescript/PROTOCOL.md`](../typescript/PROTOCOL.md)
and golden envelopes in [`../protocol/fixtures`](../protocol/fixtures).
Run tests with `python -m unittest discover -s tests`.
