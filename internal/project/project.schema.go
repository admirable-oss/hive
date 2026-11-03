package project

import (
	_ "embed"
)

var (
	//go:embed schema/hive.schema.json
	projectSchema []byte
	//go:embed schema/hive.local.schema.json
	localSchema []byte
)

// Schema is the JSON Schema of hive.toml, or with local of
// hive.local.toml, for editors.
func Schema(local bool) []byte {
	if local {
		return localSchema
	}
	return projectSchema
}
