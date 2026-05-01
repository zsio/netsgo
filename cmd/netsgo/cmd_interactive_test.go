package main

import (
	"errors"
	"testing"

	"netsgo/internal/tui"
)

func TestRunInteractiveCommandTreatsCancellationAsSuccess(t *testing.T) {
	err := runInteractiveCommand(func() error {
		return tui.ErrCancelled
	})
	if err != nil {
		t.Fatalf("interactive cancellation should return nil, got %v", err)
	}
}

func TestRunInteractiveCommandReturnsNonCancellationErrors(t *testing.T) {
	want := errors.New("boom")
	err := runInteractiveCommand(func() error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped error %v, got %v", want, err)
	}
}
