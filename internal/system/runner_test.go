package system

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recordedExec struct {
	ctx  context.Context
	name string
	args []string
}

type fakeCommand struct {
	out []byte
	err error
}

func (c fakeCommand) CombinedOutput() ([]byte, error) {
	return c.out, c.err
}

func TestRunnerRunBuildsArgvWithoutShell(t *testing.T) {
	var calls []recordedExec
	runner := Runner{
		lookPath: failLookPath(t),
		isRoot:   failIsRoot(t),
		execCmd:  recordExec(&calls, "ok", nil),
	}

	out, err := runner.Run(context.Background(), "nvidia-smi", "-i", "0", "-pl", "250")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected output: %q", out)
	}
	assertOneExec(t, calls, "nvidia-smi", []string{"-i", "0", "-pl", "250"})
}

func TestRunnerRunWithOptionsUsesPkexecForRoot(t *testing.T) {
	var calls []recordedExec
	var lookups []string
	runner := Runner{
		lookPath: lookPathFrom(map[string]bool{"pkexec": true}, &lookups),
		isRoot:   func() bool { return false },
		execCmd:  recordExec(&calls, "ok", nil),
	}

	_, err := runner.RunWithOptions(context.Background(), RunOptions{RequireRoot: true}, "nvidia-smi", "-i", "0")
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}

	assertOneExec(t, calls, "pkexec", []string{"nvidia-smi", "-i", "0"})
	assertStrings(t, lookups, []string{"pkexec"})
}

func TestRunnerRunWithOptionsFallsBackToSudoForRoot(t *testing.T) {
	var calls []recordedExec
	var lookups []string
	runner := Runner{
		lookPath: lookPathFrom(map[string]bool{"sudo": true}, &lookups),
		isRoot:   func() bool { return false },
		execCmd:  recordExec(&calls, "ok", nil),
	}

	_, err := runner.RunWithOptions(context.Background(), RunOptions{RequireRoot: true}, "systemctl", "restart", "nvidia-powerd.service")
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}

	assertOneExec(t, calls, "sudo", []string{"-n", "systemctl", "restart", "nvidia-powerd.service"})
	assertStrings(t, lookups, []string{"pkexec", "sudo"})
}

func TestRunnerRunWithOptionsSkipsElevationWhenAlreadyRoot(t *testing.T) {
	var calls []recordedExec
	runner := Runner{
		lookPath: failLookPath(t),
		isRoot:   func() bool { return true },
		execCmd:  recordExec(&calls, "ok", nil),
	}

	_, err := runner.RunWithOptions(context.Background(), RunOptions{RequireRoot: true}, "systemctl", "enable", "--now", "nvidia-powerd.service")
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}

	assertOneExec(t, calls, "systemctl", []string{"enable", "--now", "nvidia-powerd.service"})
}

func TestRunnerRunWithOptionsErrorsWhenElevationUnavailable(t *testing.T) {
	var calls []recordedExec
	var lookups []string
	runner := Runner{
		lookPath: lookPathFrom(nil, &lookups),
		isRoot:   func() bool { return false },
		execCmd:  recordExec(&calls, "ok", nil),
	}

	_, err := runner.RunWithOptions(context.Background(), RunOptions{RequireRoot: true}, "systemctl", "restart", "nvidia-powerd.service")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "pkexec/sudo not found") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("exec should not be called, got %d calls", len(calls))
	}
	assertStrings(t, lookups, []string{"pkexec", "sudo"})
}

func TestRunnerRunDoesNotShellInterpretMetacharacters(t *testing.T) {
	var calls []recordedExec
	runner := Runner{
		lookPath: lookPathFrom(map[string]bool{"pkexec": true}, nil),
		isRoot:   func() bool { return false },
		execCmd:  recordExec(&calls, "ok", nil),
	}

	_, err := runner.RunWithOptions(
		context.Background(),
		RunOptions{RequireRoot: true},
		"printf",
		"%s",
		"hello; touch /tmp/owned",
		"$(id)",
		"a b",
	)
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}

	assertOneExec(t, calls, "pkexec", []string{
		"printf",
		"%s",
		"hello; touch /tmp/owned",
		"$(id)",
		"a b",
	})
	for _, arg := range calls[0].args {
		if arg == "sh" || arg == "-c" {
			t.Fatalf("argv runner unexpectedly used shell arg %q in %v", arg, calls[0].args)
		}
	}
}

func TestRunnerRunWithOptionsAppliesTimeout(t *testing.T) {
	var calls []recordedExec
	runner := Runner{
		execCmd: recordExec(&calls, "ok", nil),
	}
	timeout := 250 * time.Millisecond
	start := time.Now()

	_, err := runner.RunWithOptions(context.Background(), RunOptions{Timeout: timeout}, "true")
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected one exec call, got %d", len(calls))
	}
	deadline, ok := calls[0].ctx.Deadline()
	if !ok {
		t.Fatal("expected context deadline")
	}
	if deadline.Before(start.Add(timeout)) {
		t.Fatalf("deadline %v is before expected timeout from %v", deadline, start)
	}
	if deadline.After(start.Add(timeout + time.Second)) {
		t.Fatalf("deadline %v is too far after expected timeout from %v", deadline, start)
	}
}

func TestRunnerRunUnsafeShellIsExplicitShellAPI(t *testing.T) {
	var calls []recordedExec
	runner := Runner{
		execCmd: recordExec(&calls, "ok", nil),
	}

	_, err := runner.RunUnsafeShell(context.Background(), "echo hello", RunOptions{})
	if err != nil {
		t.Fatalf("RunUnsafeShell returned error: %v", err)
	}

	assertOneExec(t, calls, "sh", []string{"-c", "echo hello"})
}

func recordExec(calls *[]recordedExec, output string, err error) execCommandFunc {
	return func(ctx context.Context, name string, args ...string) outputCommand {
		*calls = append(*calls, recordedExec{
			ctx:  ctx,
			name: name,
			args: append([]string(nil), args...),
		})
		return fakeCommand{out: []byte(output), err: err}
	}
}

func lookPathFrom(found map[string]bool, lookups *[]string) lookPathFunc {
	return func(name string) (string, error) {
		if lookups != nil {
			*lookups = append(*lookups, name)
		}
		if found[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func failLookPath(t *testing.T) lookPathFunc {
	t.Helper()
	return func(name string) (string, error) {
		t.Fatalf("lookPath should not be called, got %q", name)
		return "", nil
	}
}

func failIsRoot(t *testing.T) isRootFunc {
	t.Helper()
	return func() bool {
		t.Fatal("isRoot should not be called")
		return false
	}
}

func assertOneExec(t *testing.T, calls []recordedExec, name string, args []string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("expected one exec call, got %d", len(calls))
	}
	if calls[0].name != name {
		t.Fatalf("unexpected executable: got %q want %q", calls[0].name, name)
	}
	assertStrings(t, calls[0].args, args)
}

func assertStrings(t *testing.T, got []string, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected strings:\ngot:  %v\nwant: %v", got, want)
	}
}
