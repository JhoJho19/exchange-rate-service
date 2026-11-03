package main

import (
	"log"
	"os"
	"time"
)

// withShutdownDeadline keeps the watchdog alive until run and all its deferred
// cleanup have returned. startShutdown is called only by the orchestration
// goroutine; repeated calls do not extend the deadline or create another timer.
func withShutdownDeadline(timeout time.Duration, run func(startShutdown func()) error) error {
	var watchdog *time.Timer
	defer func() {
		if watchdog != nil {
			watchdog.Stop()
		}
	}()
	return run(func() {
		if watchdog == nil {
			watchdog = time.AfterFunc(timeout, func() {
				log.Printf("ERROR: Application shutdown exceeded %s, including cleanup; forcing exit with code 1", timeout)
				os.Exit(1)
			})
		}
	})
}
