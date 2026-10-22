package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestServeDrainsInFlightRequestsBeforeReturning(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		finished.Store(true)
		io.WriteString(w, "done")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 5*time.Second) }()

	body := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			body <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()
	<-started
	stop() // SIGTERM equivalent, request still in flight

	select {
	case err := <-served:
		t.Fatalf("serve returned (%v) while a request was in flight; the DB would be closed under it", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !finished.Load() {
		t.Fatal("handler did not finish before serve returned")
	}
	if got := <-body; got != "done" {
		t.Fatalf("client saw %q, want a complete response", got)
	}
}

func TestServeGivesUpAfterGraceBudget(t *testing.T) {
	started := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // never finishes on its own
	})}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 200*time.Millisecond) }()
	go http.Get("http://" + ln.Addr().String())
	<-started
	stop()
	select {
	case err := <-served:
		if err == nil {
			t.Fatal("a drain that timed out must be reported")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not give up after the grace budget")
	}
}
