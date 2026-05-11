package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type outputCommand interface {
	CombinedOutput() ([]byte, error)
}

type execCommandFunc func(context.Context, string, ...string) outputCommand
type lookPathFunc func(string) (string, error)
type isRootFunc func() bool

// RunOptions configures command execution.
type RunOptions struct {
	RequireRoot bool
	Timeout     time.Duration
}

// Runner executes commands as argv, without a shell.
type Runner struct {
	lookPath lookPathFunc
	isRoot   isRootFunc
	execCmd  execCommandFunc
}

// NewRunner returns a runner using the process environment.
func NewRunner() Runner {
	return Runner{}
}

// Run executes name with args using the default runner.
func Run(ctx context.Context, name string, args ...string) (string, error) {
	return NewRunner().Run(ctx, name, args...)
}

// RunWithOptions executes name with args using the default runner.
func RunWithOptions(ctx context.Context, options RunOptions, name string, args ...string) (string, error) {
	return NewRunner().RunWithOptions(ctx, options, name, args...)
}

// Run executes name with args.
func (r Runner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return r.RunWithOptions(ctx, RunOptions{}, name, args...)
}

// RunWithOptions executes name with args, optionally using root elevation.
func (r Runner) RunWithOptions(ctx context.Context, options RunOptions, name string, args ...string) (string, error) {
	if ctx == nil {
		return "", errors.New("context is required")
	}
	if name == "" {
		return "", errors.New("command name is required")
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	execName, execArgs, err := r.argv(options, name, args)
	if err != nil {
		return "", err
	}

	out, err := r.commandContext(ctx, execName, execArgs...).CombinedOutput()
	return string(out), err
}

func (r Runner) argv(options RunOptions, name string, args []string) (string, []string, error) {
	if options.RequireRoot && !r.root() {
		if _, err := r.path("pkexec"); err == nil {
			return "pkexec", append([]string{name}, args...), nil
		}
		if _, err := r.path("sudo"); err == nil {
			return "sudo", append([]string{"-n", name}, args...), nil
		}
		return "", nil, fmt.Errorf("root privileges required but pkexec/sudo not found")
	}
	return name, append([]string(nil), args...), nil
}

func (r Runner) path(name string) (string, error) {
	if r.lookPath != nil {
		return r.lookPath(name)
	}
	return exec.LookPath(name)
}

func (r Runner) root() bool {
	if r.isRoot != nil {
		return r.isRoot()
	}
	return IsRoot()
}

func (r Runner) commandContext(ctx context.Context, name string, args ...string) outputCommand {
	if r.execCmd != nil {
		return r.execCmd(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

func IsRoot() bool {
	return os.Geteuid() == 0
}

// RunUnsafeShell runs command through sh -c.
// Do not pass untrusted or interpolated input to command.
func RunUnsafeShell(ctx context.Context, command string, options RunOptions) (string, error) {
	return NewRunner().RunUnsafeShell(ctx, command, options)
}

// RunUnsafeShell runs command through sh -c.
// Do not pass untrusted or interpolated input to command.
func (r Runner) RunUnsafeShell(ctx context.Context, command string, options RunOptions) (string, error) {
	return r.RunWithOptions(ctx, options, "sh", "-c", command)
}
