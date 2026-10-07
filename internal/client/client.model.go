package client

type Status struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Socket    string `json:"socket"`
	StartedAt string `json:"started_at"`
}
