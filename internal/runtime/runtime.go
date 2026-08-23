// Package runtime defines the runtime-agnostic contract used by Hostix.
package runtime

import (
	"context"
	"io"
	"time"
)

// Runtime exposes only lifecycle operations shared by containers and virtual
// machines. Backend-specific image creation and provisioning stay in adapters.
type Runtime interface {
	Name() string
	Create(context.Context, CreateRequest) (Instance, error)
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Remove(context.Context, string, RemoveOptions) error
	Exec(context.Context, string, ExecRequest) (ExecResult, error)
	Logs(context.Context, string, LogsOptions) (io.ReadCloser, error)
	Status(context.Context, string) (Status, error)
	List(context.Context) ([]Status, error)
}

type CreateRequest struct {
	Name          string
	Image         string
	ProjectDir    string
	Environment   map[string]string
	Ports         []PortBinding
	CPUCount      float64
	MemoryBytes   int64
	RestartPolicy string
}

type PortBinding struct {
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
}

type Instance struct {
	ID   string
	Name string
}

type RemoveOptions struct {
	Force bool
}

type ExecRequest struct {
	Command     []string
	Environment map[string]string
	WorkingDir  string
	Interactive bool
}

type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type LogsOptions struct {
	Follow bool
	Tail   int
	Since  time.Time
}

type State string

const (
	StateCreated State = "created"
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateUnknown State = "unknown"
)

type Status struct {
	ID          string
	Name        string
	State       State
	StartedAt   time.Time
	CPUPercent  float64
	MemoryBytes int64
}
