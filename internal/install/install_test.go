package install

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"netsgo/internal/tui"
)

type fakeUI struct {
	selects   []int
	inputs    []string
	passwords []string
	confirms  []bool
	summaries []summaryCall
}

type summaryCall struct {
	title string
	rows  [][2]string
}

func (f *fakeUI) Select(prompt string, options []string) (int, error) {
	if len(f.selects) == 0 {
		return 0, errors.New("no select value")
	}
	v := f.selects[0]
	f.selects = f.selects[1:]
	return v, nil
}

func (f *fakeUI) Input(prompt string, opts ...tui.InputOptions) (string, error) {
	if len(f.inputs) == 0 {
		return "", errors.New("no input value")
	}
	v := f.inputs[0]
	f.inputs = f.inputs[1:]
	return v, nil
}

func (f *fakeUI) Password(prompt string, opts ...tui.InputOptions) (string, error) {
	if len(f.passwords) == 0 {
		return "", errors.New("no password value")
	}
	v := f.passwords[0]
	f.passwords = f.passwords[1:]
	return v, nil
}

func (f *fakeUI) Confirm(prompt string) (bool, error) {
	if len(f.confirms) == 0 {
		return false, errors.New("no confirm value")
	}
	v := f.confirms[0]
	f.confirms = f.confirms[1:]
	return v, nil
}

func (f *fakeUI) PrintSummary(title string, rows [][2]string) {
	f.summaries = append(f.summaries, summaryCall{title: title, rows: rows})
}

func TestRunWithPlatformCheck(t *testing.T) {
	err := RunWith(Deps{
		GOOS:          "darwin",
		HasTTY:        true,
		UID:           0,
		HasSystemd:    true,
		UI:            &fakeUI{},
		InstallServer: func() error { return nil },
		InstallClient: func() error { return nil },
	})
	if err == nil {
		t.Fatal("non-Linux platform should fail")
	}
}

func TestRunWithTTYCheck(t *testing.T) {
	err := RunWith(Deps{
		GOOS:          "linux",
		HasTTY:        false,
		UID:           0,
		HasSystemd:    true,
		UI:            &fakeUI{},
		InstallServer: func() error { return nil },
		InstallClient: func() error { return nil },
	})
	if err == nil {
		t.Fatal("non-TTY should fail")
	}
}

func TestRunWithRoleDispatch(t *testing.T) {
	called := ""
	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        0,
		HasSystemd: true,
		UI:         &fakeUI{selects: []int{0}},
		InstallServer: func() error {
			called = "server"
			return nil
		},
		InstallClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunWith() should not fail: %v", err)
	}
	if called != "server" {
		t.Fatalf("selecting server role should dispatch to server, got %q", called)
	}
	called = ""
	err = RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        0,
		HasSystemd: true,
		UI:         &fakeUI{selects: []int{1}},
		InstallServer: func() error {
			called = "server"
			return nil
		},
		InstallClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunWith() should not fail: %v", err)
	}
	if called != "client" {
		t.Fatalf("selecting client role should dispatch to client, got %q", called)
	}
}

func TestRunWithNonRootReexecsUsingLookedUpSudo(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"netsgo", "install"}
	t.Cleanup(func() {
		os.Args = origArgs
	})

	execErr := errors.New("exec called")
	var gotPath string
	var gotArgv []string

	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        1000,
		HasSystemd: true,
		LookPath: func(file string) (string, error) {
			if file != "sudo" {
				t.Fatalf("expected sudo lookup, got %q", file)
			}
			return "/tmp/custom/sudo", nil
		},
		Exec: func(argv0 string, argv []string, envv []string) error {
			gotPath = argv0
			gotArgv = append([]string(nil), argv...)
			return execErr
		},
	})

	if !errors.Is(err, execErr) {
		t.Fatalf("expected exec error, got %v", err)
	}
	if gotPath != "/tmp/custom/sudo" {
		t.Fatalf("expected resolved sudo path, got %q", gotPath)
	}
	wantArgv := append([]string{"sudo"}, os.Args...)
	if !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Fatalf("expected argv %v, got %v", wantArgv, gotArgv)
	}
}

func TestRunWithNonRootMissingSudoFailsClearly(t *testing.T) {
	calledExec := false
	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        1000,
		HasSystemd: true,
		LookPath: func(file string) (string, error) {
			if file != "sudo" {
				t.Fatalf("expected sudo lookup, got %q", file)
			}
			return "", exec.ErrNotFound
		},
		Exec: func(argv0 string, argv []string, envv []string) error {
			calledExec = true
			return nil
		},
	})

	if err == nil {
		t.Fatal("expected missing sudo error")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("expected wrapped exec.ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("expected actionable sudo PATH error, got %v", err)
	}
	if calledExec {
		t.Fatal("exec should not run when sudo is missing")
	}
}
