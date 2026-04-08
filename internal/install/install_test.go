package install

import (
	"errors"
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
