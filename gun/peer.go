package gun

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// ErrPeer is an error specific to a peer.
type ErrPeer struct {
	// Err is the error.
	Err error
	// Peer is the peer the error relates to.
	Peer *Peer
}

func (e *ErrPeer) Error() string { return fmt.Sprintf("Error on peer %v: %v", e.Peer, e.Err) }

// Peer is a known peer to Gun. It has a single connection. Some peers are
// "reconnectable" which means due to failure, they may be in a "bad" state
// awaiting reconnection.
type Peer struct {
	name       string
	newConn    func() (PeerConn, error)
	sleepOnErr time.Duration // TODO: would be better as backoff
	id         string

	connCurrent PeerConn
	connBad     bool // If true, don't try anything
	connLock    sync.Mutex
}

func newPeer(name string, newConn func() (PeerConn, error), sleepOnErr time.Duration) (*Peer, error) {
	p := &Peer{name: name, newConn: newConn, sleepOnErr: sleepOnErr}
	var err error
	if p.connCurrent, err = newConn(); err != nil {
		return nil, err
	}
	return p, nil
}

// ID is the identifier of the peer as given by the peer. It is empty if the
// peer wasn't asked for or didn't give an ID.
func (p *Peer) ID() string { return p.id }

// Name is the name of the peer which is usually the URL.
func (p *Peer) Name() string { return p.name }

// String is a string representation of the peer including whether it's
// connected.
func (p *Peer) String() string {
	id := ""
	if p.id != "" {
		id = "(id: " + p.id + ")"
	}
	connStatus := "disconnected"
	if conn := p.Conn(); conn != nil {
		connStatus = "connected to " + conn.RemoteURL()
	}
	return fmt.Sprintf("Peer%v %v (%v)", id, p.name, connStatus)
}

func (p *Peer) reconnectSupported() bool {
	return p.sleepOnErr > 0
}

func (p *Peer) reconnect() (err error) {
	if !p.reconnectSupported() {
		return fmt.Errorf("Reconnect not supported")
	}
	p.connLock.Lock()
	defer p.connLock.Unlock()
	if p.connCurrent == nil && p.connBad {
		p.connBad = false
		if p.connCurrent, err = p.newConn(); err != nil {
			p.connBad = true
			time.AfterFunc(p.sleepOnErr, func() { p.reconnect() })
		}
	}
	return
}

// Conn is the underlying PeerConn. This can be nil if the peer is currently
// "bad" or closed.
func (p *Peer) Conn() PeerConn {
	p.connLock.Lock()
	defer p.connLock.Unlock()
	return p.connCurrent
}

func (p *Peer) markConnErrored(conn PeerConn) {
	if !p.reconnectSupported() {
		p.Close()
		return
	}
	p.connLock.Lock()
	defer p.connLock.Unlock()
	if conn == p.connCurrent {
		p.connCurrent = nil
		p.connBad = true
		conn.Close()
		time.AfterFunc(p.sleepOnErr, func() { p.reconnect() })
	}
}

func (p *Peer) send(ctx context.Context, msg *Message, moreMsgs ...*Message) (ok bool, err error) {
	conn := p.Conn()
	if conn == nil {
		return false, nil
	}
	// Clone them with peer "to"
	updatedMsg := &Message{}
	*updatedMsg = *msg
	updatedMsg.To = conn.RemoteURL()
	updatedMoreMsgs := make([]*Message, len(moreMsgs))
	for i, moreMsg := range moreMsgs {
		updatedMoreMsg := &Message{}
		*updatedMoreMsg = *moreMsg
		updatedMoreMsg.To = conn.RemoteURL()
		updatedMoreMsgs[i] = updatedMoreMsg
	}
	if err = conn.Send(ctx, updatedMsg, updatedMoreMsgs...); err != nil {
		p.markConnErrored(conn)
		return false, err
	}
	return true, nil
}

func (p *Peer) receive(ctx context.Context) (ok bool, msgs []*Message, err error) {
	if conn := p.Conn(); conn == nil {
		return false, nil, nil
	} else if msgs, err = conn.Receive(ctx); err != nil {
		p.markConnErrored(conn)
		return false, nil, err
	} else {
		return true, msgs, nil
	}
}

// Close closes the peer and the connection is connected.
func (p *Peer) Close() error {
	p.connLock.Lock()
	defer p.connLock.Unlock()
	var err error
	if p.connCurrent != nil {
		err = p.connCurrent.Close()
		p.connCurrent = nil
	}
	p.connBad = false
	return err
}

// Closed is whether the peer is closed.
func (p *Peer) Closed() bool {
	p.connLock.Lock()
	defer p.connLock.Unlock()
	return p.connCurrent == nil && !p.connBad
}

// PeerConn is a single peer connection.
type PeerConn interface {
	// Send sends the given message (and maybe others) to the peer. The context
	// governs just this send.
	Send(ctx context.Context, msg *Message, moreMsgs ...*Message) error
	// Receive waits for the next message (or set of messages if sent at once)
	// from a peer. The context can be used to control a timeout.
	Receive(ctx context.Context) ([]*Message, error)
	// RemoteURL is the URL this peer is connected via.
	RemoteURL() string
	// Close closes this connection.
	Close() error
}

// PeerURLSchemes is the map that maps URL schemes to factory functions to
// create the connection. Currently "http" and "https" simply defer to "ws" and
// "wss" respectively. "ws" and "wss" use DialPeerConnWebSocket.
var PeerURLSchemes map[string]func(context.Context, *url.URL) (PeerConn, error)

func init() {
	PeerURLSchemes = map[string]func(context.Context, *url.URL) (PeerConn, error){
		"http": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			schemeChangedURL := &url.URL{}
			*schemeChangedURL = *peerURL
			schemeChangedURL.Scheme = "ws"
			return DialPeerConnWebSocket(ctx, schemeChangedURL)
		},
		"https": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			schemeChangedURL := &url.URL{}
			*schemeChangedURL = *peerURL
			schemeChangedURL.Scheme = "wss"
			return DialPeerConnWebSocket(ctx, schemeChangedURL)
		},
		"ws": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			return DialPeerConnWebSocket(ctx, peerURL)
		},
		"wss": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			return DialPeerConnWebSocket(ctx, peerURL)
		},
	}
}

// NewPeerConn connects to a peer for the given URL.
func NewPeerConn(ctx context.Context, peerURL string) (PeerConn, error) {
	if parsedURL, err := url.Parse(peerURL); err != nil {
		return nil, err
	} else if peerNew := PeerURLSchemes[parsedURL.Scheme]; peerNew == nil {
		return nil, fmt.Errorf("Unknown peer URL scheme %v", parsedURL.Scheme)
	} else {
		return peerNew(ctx, parsedURL)
	}
}
