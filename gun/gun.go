package gun

import (
	"context"
	"fmt"
	"sync"
)

type Gun struct {
	peers            []Peer
	storage          Storage
	soulGen          func() string
	peerErrorHandler func(*ErrPeer)

	messageIDPutListeners     map[string]chan<- *MessageReceived
	messageIDPutListenersLock sync.RWMutex
}

type Config struct {
	Peers            []Peer
	Storage          Storage
	SoulGen          func() string
	PeerErrorHandler func(*ErrPeer)
}

func New(config Config) *Gun {
	g := &Gun{
		peers:                 make([]Peer, len(config.Peers)),
		storage:               config.Storage,
		soulGen:               config.SoulGen,
		peerErrorHandler:      config.PeerErrorHandler,
		messageIDPutListeners: map[string]chan<- *MessageReceived{},
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

func (g *Gun) Close() error {
	var errs []error
	for _, p := range g.peers {
		if err := p.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	} else if len(errs) == 1 {
		return errs[0]
	} else {
		return fmt.Errorf("Multiple errors: %v", errs)
	}
}

func (g *Gun) Send(ctx context.Context, msg *Message) <-chan *ErrPeer {
	return g.send(ctx, msg, nil)
}

func (g *Gun) send(ctx context.Context, msg *Message, ignorePeer Peer) <-chan *ErrPeer {
	ch := make(chan *ErrPeer, len(g.peers))
	// Everything async
	go func() {
		defer close(ch)
		var wg sync.WaitGroup
		for _, peer := range g.peers {
			if peer == ignorePeer {
				continue
			}
			wg.Add(1)
			go func(peer Peer) {
				defer wg.Done()
				if err := peer.Send(ctx, msg); err != nil {
					peerErr := &ErrPeer{err, peer}
					go g.onPeerError(peerErr)
					ch <- peerErr
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
				if msgOrErr.Err != nil {
					go g.onPeerError(&ErrPeer{msgOrErr.Err, peer})
					continue
				}
				// See if a listener is around to handle it instead of rebroadcasting
				msg := &MessageReceived{Message: msgOrErr.Message, Peer: peer}
				if msg.Ack != "" && len(msg.Put) > 0 {
					g.messageIDPutListenersLock.RLock()
					l := g.messageIDPutListeners[msg.Ack]
					g.messageIDPutListenersLock.RUnlock()
					if l != nil {
						go safeReceivedMessageSend(l, msg)
						continue
					}
				}
				go g.onUnhandledMessage(msg)
			}
		}(peer)
	}
}

func (g *Gun) onUnhandledMessage(msg *MessageReceived) {
	// Unhandled message means rebroadcast
	// TODO: we need a timeout or global context here...
	g.send(context.TODO(), msg.Message, msg.Peer)
}

func (g *Gun) onPeerError(err *ErrPeer) {
	if g.peerErrorHandler != nil {
		g.peerErrorHandler(err)
	}
}

func (g *Gun) RegisterMessageIDPutListener(id string, ch chan<- *MessageReceived) {
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
	s := newScoped(g, nil, key)
	if len(children) > 0 {
		s = s.Scoped(ctx, children[0], children[1:]...)
	}
	return s
}

func safeReceivedMessageSend(ch chan<- *MessageReceived, msg *MessageReceived) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- msg
}
