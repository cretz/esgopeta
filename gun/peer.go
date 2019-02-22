package gun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

type ErrPeer struct {
	Err  error
	peer *gunPeer
}

func (e *ErrPeer) Error() string { return fmt.Sprintf("Error on peer %v: %v", e.peer, e.Err) }

type Peer interface {
	Send(ctx context.Context, msg *Message, moreMsgs ...*Message) error
	// Chan is closed on first err, when context is closed, or when peer is closed
	Receive(ctx context.Context) ([]*Message, error)
	Close() error
}

var PeerURLSchemes = map[string]func(context.Context, *url.URL) (Peer, error){
	"http": func(ctx context.Context, peerURL *url.URL) (Peer, error) {
		schemeChangedURL := &url.URL{}
		*schemeChangedURL = *peerURL
		schemeChangedURL.Scheme = "ws"
		return NewPeerWebSocket(ctx, schemeChangedURL)
	},
	"ws": func(ctx context.Context, peerURL *url.URL) (Peer, error) {
		return NewPeerWebSocket(ctx, peerURL)
	},
}

func NewPeer(ctx context.Context, peerURL string) (Peer, error) {
	if parsedURL, err := url.Parse(peerURL); err != nil {
		return nil, err
	} else if peerNew := PeerURLSchemes[parsedURL.Scheme]; peerNew == nil {
		return nil, fmt.Errorf("Unknown peer URL scheme %v", parsedURL.Scheme)
	} else {
		return peerNew(ctx, parsedURL)
	}
}

type PeerWebSocket struct {
	Underlying *websocket.Conn
	WriteLock  sync.Mutex
}

func NewPeerWebSocket(ctx context.Context, peerUrl *url.URL) (*PeerWebSocket, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, peerUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	return &PeerWebSocket{Underlying: conn}, nil
}

func (p *PeerWebSocket) Send(ctx context.Context, msg *Message, moreMsgs ...*Message) error {
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

func (p *PeerWebSocket) Receive(ctx context.Context) ([]*Message, error) {
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

func (p *PeerWebSocket) Close() error {
	return p.Underlying.Close()
}
