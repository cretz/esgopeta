package gun

import (
	"context"
)

// Server is the interface implemented by servers.
type Server interface {
	// Serve is called by Gun to start the server. It should not return until
	// an error occurs or Close is called.
	Serve() error
	// Accept is called to wait for the next peer connection or if an error
	// occurs while trying.
	Accept() (PeerConn, error)
	// Close is called by Gun to stop and close this server.
	Close() error
}

func (g *Gun) startServers(ctx context.Context, servers []Server) {
	ctx, g.serversCancelFn = context.WithCancel(ctx)
	for _, server := range servers {
		// TODO: log error?
		go g.serve(ctx, server)
	}
}

func (g *Gun) serve(ctx context.Context, s Server) error {
	errCh := make(chan error, 1)
	ctx, cancelFn := context.WithCancel(ctx)
	defer cancelFn()
	// Start the server
	go func() { errCh <- s.Serve() }()
	defer s.Close()
	// Accept connections and break off
	go func() {
		if conn, err := s.Accept(); err == nil {
			// TODO: log error (for accept and handle)?
			go g.onNewPeerConn(ctx, conn)
		}
	}()
	// Wait for server close or context close
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *Gun) onNewPeerConn(ctx context.Context, conn PeerConn) error {
	ctx, cancelFn := context.WithCancel(ctx)
	defer cancelFn()
	defer conn.Close()
	// We always send a DAM req first
	if err := conn.Send(ctx, &Message{DAM: "?"}); err != nil {
		return err
	}
	// Now add the connection to Gun
	panic("TODO")
}
