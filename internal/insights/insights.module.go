// Package insights turns what Hive sees of agents into time and usage:
// how long each agent spent working, blocked, idle or done (per agent,
// kind, environment, task and day), how long it waited on a person and
// what it asked, and the tokens and cost agents report, with the source of
// each number. Budgets on cost, tokens or working time per day, plan,
// environment or kind warn as they near their limits, and can hold new
// tasks once over.
//
// Everything is kept in the Hive root, under the retention settings: one
// file of JSON lines per local day, and a small state file. Nothing is
// sent anywhere; usage nobody reported is unknown, never estimated.
package insights

import "path/filepath"

// ModuleConfig wires the package.
type ModuleConfig struct {
	// BaseDir is the session's root: insights are kept in
	// <BaseDir>/insights.
	BaseDir string
	Service Config
}

// Module is the package's wiring.
type Module struct {
	Service *Service
}

// NewModule builds the service over a store in the Hive root.
func NewModule(cfg ModuleConfig, ports Ports) *Module {
	return &Module{Service: NewService(cfg.Service, NewStore(Dir(cfg.BaseDir)), ports)}
}

// Dir is where insights are kept under a Hive root.
func Dir(base string) string { return filepath.Join(base, "insights") }
