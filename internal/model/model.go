package model

import "time"

const SchemaVersion = 1

type Status string

const (
	StatusSuccess Status = "success"
	StatusSkipped Status = "skipped"
	StatusBlocked Status = "blocked"
	StatusFailed  Status = "failed"
)

type ToolInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

type RunInfo struct {
	ID        string     `json:"id"`
	Profile   string     `json:"profile"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

type HostInfo struct {
	Label       string `json:"label"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	LogicalCPUs int    `json:"logical_cpus"`
	CPUModel    string `json:"cpu_model,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
}

type Plan struct {
	Categories               []string          `json:"categories"`
	EstimatedTransferBytes   uint64            `json:"estimated_transfer_bytes"`
	EstimatedRequests        uint64            `json:"estimated_requests"`
	EstimatedDurationSeconds uint64            `json:"estimated_duration_seconds,omitempty"`
	ExternalDataSharing      bool              `json:"external_data_sharing,omitempty"`
	Resources                []PlannedResource `json:"resources,omitempty"`
	Engines                  []PlannedEngine   `json:"engines,omitempty"`
}

type PlannedResource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type PlannedEngine struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
}

type Metric struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Direction string  `json:"direction,omitempty"`
}

type Benchmark struct {
	ID                string         `json:"id"`
	Category          string         `json:"category"`
	Status            Status         `json:"status"`
	Engine            string         `json:"engine"`
	EngineVersion     string         `json:"engine_version,omitempty"`
	WorkloadVersion   string         `json:"workload_version,omitempty"`
	Comparable        bool           `json:"comparable"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	Metrics           []Metric       `json:"metrics"`
	Warnings          []string       `json:"warnings"`
	ErrorCode         string         `json:"error_code,omitempty"`
	Error             string         `json:"error,omitempty"`
	RawArtifact       string         `json:"raw_artifact,omitempty"`
	RawArtifactSHA256 string         `json:"raw_artifact_sha256,omitempty"`
}

type Resource struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Owned         bool   `json:"owned"`
	CleanupStatus string `json:"cleanup_status"`
	Error         string `json:"error,omitempty"`
}

type Result struct {
	SchemaVersion int         `json:"schema_version"`
	Tool          ToolInfo    `json:"tool"`
	Run           RunInfo     `json:"run"`
	Host          HostInfo    `json:"host"`
	Plan          Plan        `json:"plan"`
	Benchmarks    []Benchmark `json:"benchmarks"`
	Resources     []Resource  `json:"resources"`
	Warnings      []string    `json:"warnings"`
}
