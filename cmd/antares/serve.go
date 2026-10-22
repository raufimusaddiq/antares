package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// serve runs srv on ln until ctx is cancelled, then drains in-flight requests
// for at most grace. It returns only after Shutdown has finished: Serve returns
// http.ErrServerClosed as soon as Shutdown *begins*, so returning on that would
// let the caller close the database under live requests.
func serve(ctx context.Context, srv *http.Server, ln net.Listener, grace time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err // failed to serve; nothing to drain
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("shutdown: drain did not finish within %s: %w", grace, err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
