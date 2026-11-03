package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownHTTPServer(t *testing.T) {
	for _, forced := range []bool{false, true} {
		name := "graceful"
		if forced {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
				}
				_, _ = io.WriteString(w, "done")
				close(finished)
			}))
			defer server.Close()
			clientDone := make(chan error, 1)
			go func() {
				response, err := server.Client().Get(server.URL)
				if err == nil {
					body, readErr := io.ReadAll(response.Body)
					response.Body.Close()
					err = readErr
					if !forced && string(body) != "done" {
						err = errors.New("active response was interrupted")
					}
				}
				clientDone <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if forced {
				expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer expire()
				err := shutdownHTTPServer(expired, server.Config)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("shutdown error=%v", err)
				}
			} else {
				admissionEnded := make(chan struct{})
				server.Config.RegisterOnShutdown(func() { close(admissionEnded) })
				stopped := make(chan error, 1)
				go func() { stopped <- shutdownHTTPServer(ctx, server.Config) }()
				select {
				case <-admissionEnded:
				case <-ctx.Done():
					t.Fatal("shutdown did not start")
				}
				select {
				case err := <-stopped:
					t.Fatalf("shutdown returned before active request finished: %v", err)
				default:
				}
				close(release)
				select {
				case err := <-stopped:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("shutdown did not finish")
				}
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("handler did not finish")
			}
			select {
			case err := <-clientDone:
				if !forced && err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("client did not finish")
			}
			if response, err := server.Client().Get(server.URL); err == nil {
				response.Body.Close()
				t.Fatal("server accepted request after shutdown")
			}
		})
	}
}
