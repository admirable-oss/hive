package environment

// ViewModel represents the environment data for UI/CLI consumption
type ViewModel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}
