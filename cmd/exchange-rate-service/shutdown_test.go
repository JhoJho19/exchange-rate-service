package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Exercise the real os.Exit in a child process, never in the parent test runner.
func TestShutdownDeadline(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		forced bool
	}{
		{"graceful", false},
		{"deadline", true},
		{"deferred", true},
		{"startup-cleanup", true},
		{"disabled", false},
		{"idle", false},
		{"repeated", true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShutdownDeadlineProcess$")
			cmd.Env = append(os.Environ(), "SHUTDOWN_WATCHDOG_TEST="+tc.mode)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("child hung: %s", output)
			}
			if tc.forced {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("expected exit 1, got %v: %s", err, output)
				}
				if !strings.Contains(string(output), "Application shutdown exceeded 1s, including cleanup; forcing exit with code 1") {
					t.Fatalf("missing watchdog diagnostic: %s", output)
				}
				if (tc.mode == "deferred" || tc.mode == "startup-cleanup") && !strings.Contains(string(output), "entered deferred cleanup") {
					t.Fatalf("deferred cleanup was not reached: %s", output)
				}
			} else if err != nil {
				t.Fatalf("unexpected termination: %v: %s", err, output)
			}
		})
	}
}

func TestShutdownDeadlineProcess(t *testing.T) {
	mode := os.Getenv("SHUTDOWN_WATCHDOG_TEST")
	if mode == "" {
		return
	}
	synctest.Test(t, func(t *testing.T) {
		cleaned := false
		wantErr := errors.New("application result")
		err := withShutdownDeadline(time.Second, func(startShutdown func()) error {
			defer func() {
				// This also models cleanup on an early startup error, before
				// the explicit shutdown path has started the watchdog.
				startShutdown()
				if mode == "deferred" || mode == "startup-cleanup" {
					fmt.Fprintln(os.Stderr, "entered deferred cleanup")
					select {}
				}
				time.Sleep(100 * time.Millisecond)
				cleaned = true
			}()
			if mode == "startup-cleanup" {
				return wantErr
			}
			if mode == "idle" {
				time.Sleep(2 * time.Second) // No deadline during normal execution.
			}
			startShutdown()
			switch mode {
			case "deadline":
				select {}
			case "repeated":
				time.Sleep(750 * time.Millisecond)
				startShutdown()
				time.Sleep(500 * time.Millisecond)
				t.Fatal("repeated start extended the deadline")
			default:
				time.Sleep(100 * time.Millisecond)
			}
			return wantErr
		})
		if !cleaned || !errors.Is(err, wantErr) {
			t.Fatalf("cleanup/result lost: cleaned=%v err=%v", cleaned, err)
		}
		if mode == "disabled" {
			time.Sleep(2 * time.Second) // A live watchdog would exit the child.
		}
	})
}
