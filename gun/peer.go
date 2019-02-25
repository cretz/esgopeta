package gun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ErrPeer struct {
	Err  error
	Peer *Peer
}

func (e *ErrPeer) Error() string { return fmt.Sprintf("Error on peer %v: %v", e.Peer, e.Err) }

type Peer struct {
	url        string
	newConn    func() (PeerConn, error)
	sleepOnErr time.Duration // TODO: would be better as backoff
	id         string

	connCurrent PeerConn
	connBad     bool // If true, don't try anything
	connLock    sync.Mutex
}

func newPeer(url string, newConn func() (PeerConn, error), sleepOnErr time.Duration) (*Peer, error) {
	p := &Peer{url: url, newConn: newConn, sleepOnErr: sleepOnErr}
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
	connStatus := "connected"
	if p.Conn() == nil {
		connStatus = "disconnected"
	}
	return fmt.Sprintf("Peer%v %v (%v)", id, p.url, connStatus)
}

func (p *Peer) reconnect() (err error) {
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
	updatedMsg.To = p.url
	updatedMoreMsgs := make([]*Message, len(moreMsgs))
	for i, moreMsg := range moreMsgs {
		updatedMoreMsg := &Message{}
		*updatedMoreMsg = *moreMsg
		updatedMoreMsg.To = p.url
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
	// Chan is closed on first err, when context is closed, or when peer is closed
	Receive(ctx context.Context) ([]*Message, error)
	Close() error
}

var PeerURLSchemes map[string]func(context.Context, *url.URL) (PeerConn, error)

func init() {
	PeerURLSchemes = map[string]func(context.Context, *url.URL) (PeerConn, error){
		"http": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			schemeChangedURL := &url.URL{}
			*schemeChangedURL = *peerURL
			schemeChangedURL.Scheme = "ws"
			return NewPeerConnWebSocket(ctx, schemeChangedURL)
		},
		"ws": func(ctx context.Context, peerURL *url.URL) (PeerConn, error) {
			return NewPeerConnWebSocket(ctx, peerURL)
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

type PeerConnWebSocket struct {
	Underlying *websocket.Conn
	WriteLock  sync.Mutex
}

func NewPeerConnWebSocket(ctx context.Context, peerUrl *url.URL) (*PeerConnWebSocket, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, peerUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	return &PeerConnWebSocket{Underlying: conn}, nil
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

func (p *PeerConnWebSocket) Close() error {
	return p.Underlying.Close()
}
