package gun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

type serverWebSocket struct {
	server         *http.Server
	acceptCh       chan *websocket.Conn
	acceptCtx      context.Context
	acceptCancelFn context.CancelFunc
	serveErrCh     chan error
}

func NewServerWebSocket(server *http.Server, upgrader *websocket.Upgrader) Server {
	if upgrader == nil {
		upgrader = &websocket.Upgrader{}
	}
	s := &serverWebSocket{
		server:     server,
		acceptCh:   make(chan *websocket.Conn),
		serveErrCh: make(chan error, 1),
	}
	// Setup the accepter
	s.acceptCtx, s.acceptCancelFn = context.WithCancel(context.Background())
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			if server.ErrorLog != nil {
				server.ErrorLog.Printf("Failed upgrading websocket: %v", err)
			}
			return
		}
		select {
		case <-s.acceptCtx.Done():
		case s.acceptCh <- conn:
		}
	})
	return s
}

func (s *serverWebSocket) Serve() error {
	return s.server.ListenAndServe()
}

func (s *serverWebSocket) Accept() (PeerConn, error) {
	select {
	case <-s.acceptCtx.Done():
		return nil, http.ErrServerClosed
	case conn := <-s.acceptCh:
		return NewPeerConnWebSocket(conn), nil
	}
}

func (s *serverWebSocket) Close() error {
	s.acceptCancelFn()
	return s.server.Close()
}

type PeerConnWebSocket struct {
	Underlying *websocket.Conn
	WriteLock  sync.Mutex
}

func DialPeerConnWebSocket(ctx context.Context, peerUrl *url.URL) (*PeerConnWebSocket, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, peerUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	return NewPeerConnWebSocket(conn), nil
}

func NewPeerConnWebSocket(underlying *websocket.Conn) *PeerConnWebSocket {
	return &PeerConnWebSocket{Underlying: underlying}
}

func (p *PeerConnWebSocket) Send(ctx context.Context, msg *Message, moreMsgs ...*Message) error {
	// If there are more, send all as an array of JSON strings, otherwise just the msg
	var toWrite interface{}
	if len(moreMsgs) == 0 {
		toWrite = msg
	} else {
		b, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		msgs := []string{string(b)}
		for _, nextMsg := range moreMsgs {
			if b, err = json.Marshal(nextMsg); err != nil {
				return err
			}
			msgs = append(msgs, string(b))
		}
		toWrite = msgs
	}
	// Send async so we can wait on context
	errCh := make(chan error, 1)
	go func() {
		p.WriteLock.Lock()
		defer p.WriteLock.Unlock()
		errCh <- p.Underlying.WriteJSON(toWrite)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *PeerConnWebSocket) Receive(ctx context.Context) ([]*Message, error) {
	bytsCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		if _, b, err := p.Underlying.ReadMessage(); err != nil {
			errCh <- err
		} else {
			bytsCh <- b
		}
	}()
	select {
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case byts := <-bytsCh:
		// If it's a JSON array, it means it's an array of JSON strings, otherwise it's one message
		if byts[0] != '[' {
			var msg Message
			if err := json.Unmarshal(byts, &msg); err != nil {
				return nil, err
			}
			return []*Message{&msg}, nil
		}
		var jsonStrs []string
		if err := json.Unmarshal(byts, &jsonStrs); err != nil {
			return nil, err
		}
		msgs := make([]*Message, len(jsonStrs))
		for i, jsonStr := range jsonStrs {
			if err := json.Unmarshal([]byte(jsonStr), &(msgs[i])); err != nil {
				return nil, err
			}
		}
		return msgs, nil
	}
}

func (p *PeerConnWebSocket) RemoteURL() string {
	return fmt.Sprintf("http://%v", p.Underlying.RemoteAddr())
}

func (p *PeerConnWebSocket) Close() error {
	return p.Underlying.Close()
}
