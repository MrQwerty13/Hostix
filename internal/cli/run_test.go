package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/MrQwerty13/Hostix/internal/app"
	hostruntime "github.com/MrQwerty13/Hostix/internal/runtime"
)

type fakeRunService struct {
	request app.RunRequest
	result  app.RunResult
	err     error
	closed  bool
}

func (f *fakeRunService) Run(_ context.Context, request app.RunRequest) (app.RunResult, error) {
	f.request = request
	return f.result, f.err
}

func (f *fakeRunService) Close() error {
	f.closed = true
	return nil
}

func TestRunCommandPassesOptionsAndCommand(t *testing.T) {
	service := &fakeRunService{result: app.RunResult{
		Instance: hostruntime.Instance{ID: strings.Repeat("a", 64), Name: "hostix-api-123"},
		ImageRef: "hostix/api:123",
		Replaced: true,
	}}
	factoryCalls := 0
	cmd := newRunCommand(func(io.Writer) (runService, error) {
		factoryCalls++
		return service, nil
	})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{
		"./api", "--stack", "python", "--runtime", "docker",
		"--name", "custom-api", "-p", "8080:8000", "-e", "MODE=dev",
		"--", "python", "main.py",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if factoryCalls != 1 || !service.closed {
		t.Fatalf("factoryCalls=%d closed=%v", factoryCalls, service.closed)
	}
	if got, want := service.request.ProjectDir, "./api"; got != want {
		t.Fatalf("ProjectDir = %q, want %q", got, want)
	}
	if got, want := service.request.Command, []string{"python", "main.py"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Command = %#v, want %#v", got, want)
	}
	if service.request.Environment["MODE"] != "dev" || service.request.Ports[0].HostPort != 8080 {
		t.Fatalf("request = %#v", service.request)
	}
	if !strings.Contains(output.String(), "Container hostix-api-123 is running") || !strings.Contains(output.String(), strings.Repeat("a", 12)) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunCommandRejectsInvalidOptionsBeforeCreatingRuntime(t *testing.T) {
	factoryCalled := false
	cmd := newRunCommand(func(io.Writer) (runService, error) {
		factoryCalled = true
		return nil, errors.New("unexpected")
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{".", "--port", "invalid"})

	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("Execute() unexpectedly succeeded")
	}
	if factoryCalled {
		t.Fatal("runtime factory called for invalid options")
	}
}

func TestRunCommandRequiresSeparatorBeforeCommand(t *testing.T) {
	cmd := newRunCommand(func(io.Writer) (runService, error) {
		return &fakeRunService{}, nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{".", "python", "main.py"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "after --") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRunCommandClosesServiceAfterFailure(t *testing.T) {
	service := &fakeRunService{err: errors.New("run failed")}
	cmd := newRunCommand(func(io.Writer) (runService, error) { return service, nil })
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"."})

	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("Execute() unexpectedly succeeded")
	}
	if !service.closed {
		t.Fatal("service was not closed after failure")
	}
}
