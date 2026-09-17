// Package placement owns explicit operator-managed execution placement retirement.
// It does not authorize Core dispatch, release harnesses or replay native work.
package placement

import "time"

// BindingLabel marks a container explicitly created for operator enrollment.
const BindingLabel = "parsar.runtime.placement"

// Receipt is durable local evidence for one exact placement incarnation.
// Only State == "retired" reports qualified settlement; other states are unknown.
type Receipt struct {
	EnvironmentID string     `json:"environment_id,omitempty"`
	Version       int        `json:"version"`
	State         string     `json:"state"`
	Owner         string     `json:"owner"`
	Target        Target     `json:"target"`
	Members       []Process  `json:"members"`
	RequestedAt   time.Time  `json:"requested_at"`
	RetiredAt     *time.Time `json:"retired_at,omitempty"`
}

// Target binds local supervisor, immutable container and observed incarnation.
type Target struct {
	HostBootID string  `json:"host_boot_id"`
	Supervisor string  `json:"supervisor"`
	Container  string  `json:"container"`
	Created    string  `json:"created"`
	Started    string  `json:"started"`
	Restarts   int     `json:"restarts"`
	Image      string  `json:"image"`
	Workspace  string  `json:"workspace"`
	Cgroup     string  `json:"cgroup"`
	Init       Process `json:"init"`
}

// Process includes Linux start ticks to distinguish a reused PID.
type Process struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}
