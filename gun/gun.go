package gun

import (
	"context"
	"fmt"
	"sync"
)

type Gun struct {
	peers   []Peer
	storage Storage
	soulGen func() string

	messageIDPutListeners     map[string]chan<- *ReceivedMessage
	messageIDPutListenersLock sync.RWMutex
}

type Config struct {
	Peers   []Peer
	Storage Storage
	SoulGen func() string
}

func New(config Config) *Gun {
	g := &Gun{
		peers:                 make([]Peer, len(config.Peers)),
		storage:               config.Storage,
		soulGen:               config.SoulGen,
		messageIDPutListeners: map[string]chan<- *ReceivedMessage{},
	}
	// Copy over peers
	copy(g.peers, config.Peers)
	// Set defaults
	if g.storage == nil {
		g.storage = &StorageInMem{}
	}
	if g.soulGen == nil {
		g.soulGen = SoulGenDefault
	}
	// Start receiving
	g.startReceiving()
	return g
}

// To note: Fails on even one peer failure (otherwise, do this yourself). May connect to
// some peers temporarily until first failure, but closes them all on failure
func NewFromPeerURLs(ctx context.Context, peerURLs ...string) (g *Gun, err error) {
	c := Config{Peers: make([]Peer, len(peerURLs))}
	for i := 0; i < len(peerURLs) && err == nil; i++ {
		if c.Peers[i], err = NewPeer(ctx, peerURLs[i]); err != nil {
			err = fmt.Errorf("Failed connecting to peer %v: %v", peerURLs[i], err)
		}
	}
	if err != nil {
		for _, peer := range c.Peers {
			if peer != nil {
				peer.Close()
			}
		}
		return
	}
	return New(c), nil
}

type Message struct {
	Ack    string             `json:"@,omitEmpty"`
	ID     string             `json:"#,omitEmpty"`
	Sender string             `json:"><,omitEmpty"`
	Hash   string             `json:"##,omitempty"`
	How    string             `json:"how,omitempty"`
	Get    *MessageGetRequest `json:"get,omitempty"`
	Put    map[string]*Node   `json:"put,omitempty"`
	DAM    string             `json:"dam,omitempty"`
	PID    string             `json:"pid,omitempty"`
}

type MessageGetRequest struct {
	ID    string `json:"#,omitempty"`
	Field string `json:".,omitempty"`
}

type ReceivedMessage struct {
	*Message
	Peer Peer
}

type PeerError struct {
	Err  error
	Peer Peer
}

func (g *Gun) Send(ctx context.Context, msg *Message) <-chan *PeerError {
	ch := make(chan *PeerError, len(g.peers))
	// Everything async
	go func() {
		defer close(ch)
		var wg sync.WaitGroup
		for _, peer := range g.peers {
			wg.Add(1)
			go func(peer Peer) {
				defer wg.Done()
				if err := peer.Send(ctx, msg); err != nil {
					ch <- &PeerError{err, peer}
				}
			}(peer)
		}
		wg.Wait()
	}()
	return ch
}

func (g *Gun) startReceiving() {
	for _, peer := range g.peers {
		go func(peer Peer) {
			for msgOrErr := range peer.Receive() {
				// TODO: what to do with error?
				if msgOrErr.Err != nil {
					g.onPeerReceiveError(&PeerError{msgOrErr.Err, peer})
					continue
				}
				// See if a listener is around to handle it instead of rebroadcasting
				msg := &ReceivedMessage{Message: msgOrErr.Message, Peer: peer}
				if msg.Ack != "" && len(msg.Put) > 0 {
					g.messageIDPutListenersLock.RLock()
					l := g.messageIDPutListeners[msg.Ack]
					g.messageIDPutListenersLock.RUnlock()
					if l != nil {
						safeReceivedMessageSend(l, msg)
						continue
					}
				}
				g.onUnhandledMessage(msg)
			}
		}(peer)
	}
}

func (g *Gun) onUnhandledMessage(msg *ReceivedMessage) {

}

func (g *Gun) onPeerReceiveError(err *PeerError) {

}

func (g *Gun) RegisterMessageIDPutListener(id string, ch chan<- *ReceivedMessage) {
	g.messageIDPutListenersLock.Lock()
	defer g.messageIDPutListenersLock.Unlock()
	g.messageIDPutListeners[id] = ch
}

func (g *Gun) UnregisterMessageIDPutListener(id string) {
	g.messageIDPutListenersLock.Lock()
	defer g.messageIDPutListenersLock.Unlock()
	delete(g.messageIDPutListeners, id)
}

// func (g *Gun) RegisterValueIDPutListener(id string, ch chan<- *ReceivedMessage) {
// 	panic("TODO")
// }

func (g *Gun) Scoped(ctx context.Context, key string, children ...string) *Scoped {
	s := newScoped(g, "", key)
	if len(children) > 0 {
		s = s.Scoped(ctx, children[0], children[1:]...)
	}
	return s
}

func safeReceivedMessageSend(ch chan<- *ReceivedMessage, msg *ReceivedMessage) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- msg
}
