package gun

import (
	"context"
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"
)

type ErrPeer struct {
	Err  error
	Peer Peer
}

func (e *ErrPeer) Error() string { return fmt.Sprintf("Error on peer %v: %v", e.Peer, e.Err) }

type Peer interface {
	Send(ctx context.Context, msg *Message) error
	Receive() <-chan *MessageOrError
	Close() error
}

type MessageOrError struct {
	Message *Message
	Err     error
}

var PeerURLSchemes = map[string]func(context.Context, *url.URL) (Peer, error){
	"ws": func(ctx context.Context, peerUrl *url.URL) (Peer, error) { return NewPeerWebSocket(ctx, peerUrl) },
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
	*websocket.Conn
}

func NewPeerWebSocket(ctx context.Context, peerUrl *url.URL) (*PeerWebSocket, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, peerUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	return &PeerWebSocket{conn}, nil
}

func (p *PeerWebSocket) Send(ctx context.Context, msg *Message) error {
	panic("TODO")
}

func (p *PeerWebSocket) Receive() <-chan *MessageOrError {
	panic("TODO")
}
