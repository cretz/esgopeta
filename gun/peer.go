package gun

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"
)

type ErrPeer struct {
	Err  error
	Peer *Peer
}

func (e *ErrPeer) Error() string { return fmt.Sprintf("Error on peer %v: %v", e.Peer, e.Err) }

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

func (p *Peer) ID() string { return p.id }

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

// Can be nil peer if currently bad or closed
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
	} else {
		return true, nil
	}
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

func (p *Peer) Closed() bool {
	p.connLock.Lock()
	defer p.connLock.Unlock()
	return p.connCurrent == nil && !p.connBad
}

type PeerConn interface {
	Send(ctx context.Context, msg *Message, moreMsgs ...*Message) error
	Receive(ctx context.Context) ([]*Message, error)
	RemoteURL() string
	Close() error
}

var PeerURLSchemes map[string]func(context.Context, *url.URL) (PeerConn, error)

func init() {
	PeerURLSchemes = map[string]func(context.Context, *url.URL) (PeerConn, error){
		"http": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			schemeChangedURL := &url.URL{}
			*schemeChangedURL = *peerURL
			schemeChangedURL.Scheme = "ws"
			return DialPeerConnWebSocket(ctx, schemeChangedURL)
		},
		"ws": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			return DialPeerConnWebSocket(ctx, peerURL)
		},
	}
}

func NewPeerConn(ctx context.Context, peerURL string) (PeerConn, error) {
	if parsedURL, err := url.Parse(peerURL); err != nil {
		return nil, err
	} else if peerNew := PeerURLSchemes[parsedURL.Scheme]; peerNew == nil {
		return nil, fmt.Errorf("Unknown peer URL scheme %v", parsedURL.Scheme)
	} else {
		return peerNew(ctx, parsedURL)
	}
}
