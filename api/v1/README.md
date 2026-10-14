# api/v1: Hive's frozen public surface

These files are what Hive 1.x promises not to break (see
[the compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/)). Tests compare the code
with them on every run:

| File | Surface | Checked by |
|---|---|---|
| `schema.json` | the wire API as `hive api schema` prints it: every method, its params and result as JSON Schema | `internal/runtime/tests` `TestAPIFrozen` |
| `api.txt` | the same, one line per method, field and type | `TestAPIFrozen` |
| `cli.txt` | every command, alias and flag (with its type) and the exit codes | `cmd/hive` `TestCLIFrozen` |
| `config.txt` | every configuration key | `internal/config` `TestConfigKeysFrozen` |
| `hiveapi.txt` | the Go SDK (`pkg/hiveapi`): exported types, fields, functions and methods with their signatures | `pkg/hiveapi` `TestSDKFrozen` |

A line that disappears or changes is a breaking change, and the test fails.
A new line, an addition, is allowed, but the files must be updated so the
addition is reviewed with the change that makes it:

```sh
HIVE_UPDATE_SURFACE=1 go test ./... -run Frozen
git diff api/v1
```
