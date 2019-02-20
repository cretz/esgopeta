package gun

import (
	"context"
	"net/url"

	"github.com/gorilla/websocket"
)

type Peer interface {
	Close() error
}

var PeerURLSchemes = map[string]func(context.Context, *url.URL) (Peer, error){
	"ws": func(ctx context.Context, peerUrl *url.URL) (Peer, error) { return NewPeerWebSocket(ctx, peerUrl) },
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
