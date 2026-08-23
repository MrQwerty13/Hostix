package app

import hostruntime "github.com/MrQwerty13/Hostix/internal/runtime"

// RunRequest describes one project run initiated by a CLI or another client.
type RunRequest struct {
	ProjectDir  string
	Stack       string
	Runtime     string
	Name        string
	Command     []string
	Environment map[string]string
	Ports       []hostruntime.PortBinding
}

// RunResult identifies the image and runtime instance created for a project.
type RunResult struct {
	Instance  hostruntime.Instance
	ImageRef  string
	Framework string
	Replaced  bool
}
