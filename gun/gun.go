package gun

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Gun struct {
	peers            []*gunPeer
	storage          Storage
	soulGen          func() string
	peerErrorHandler func(*ErrPeer)
	peerSleepOnError time.Duration

	messageIDPutListeners     map[string]chan<- *MessageReceived
	messageIDPutListenersLock sync.RWMutex
}

type Config struct {
	PeerURLs         []string
	Storage          Storage
	SoulGen          func() string
	PeerErrorHandler func(*ErrPeer)
	PeerSleepOnError time.Duration
}

const DefaultPeerSleepOnError = 30 * time.Second

func New(ctx context.Context, config Config) (*Gun, error) {
	g := &Gun{
		peers:                 make([]*gunPeer, len(config.PeerURLs)),
		storage:               config.Storage,
		soulGen:               config.SoulGen,
		peerErrorHandler:      config.PeerErrorHandler,
		peerSleepOnError:      config.PeerSleepOnError,
		messageIDPutListeners: map[string]chan<- *MessageReceived{},
	}
	// Create all the peers
	sleepOnError := config.PeerSleepOnError
	if sleepOnError == 0 {
		sleepOnError = DefaultPeerSleepOnError
	}
	var err error
	for i := 0; i < len(config.PeerURLs) && err == nil; i++ {
		peerURL := config.PeerURLs[i]
		connPeer := func() (Peer, error) { return NewPeer(ctx, peerURL) }
		if g.peers[i], err = newGunPeer(connPeer, sleepOnError); err != nil {
			err = fmt.Errorf("Failed connecting to peer %v: %v", peerURL, err)
		}
	}
	// If there was an error, we need to close what we did create
	if err != nil {
		for _, peer := range g.peers {
			if peer != nil {
				peer.Close()
			}
		}
		return nil, err
	}
	// Set defaults
	if g.storage == nil {
		g.storage = &StorageInMem{}
	}
	if g.soulGen == nil {
		g.soulGen = SoulGenDefault
	}
	// Start receiving
	g.startReceiving()
	return g, nil
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

func (g *Gun) send(ctx context.Context, msg *Message, ignorePeer *gunPeer) <-chan *ErrPeer {
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
			go func(peer *gunPeer) {
				defer wg.Done()
				// Just do nothing if the peer is bad and we couldn't send
				if _, err := peer.send(ctx, msg); err != nil {
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
		go func(peer *gunPeer) {
			// TDO: some kind of overall context is probably needed
			ctx, cancelFn := context.WithCancel(context.TODO())
			defer cancelFn()
			for {
				// We might not be able receive because peer is sleeping from
				// an error happened within or a just-before send error.
				if ok, msgs, err := peer.receive(ctx); !ok {
					if err != nil {
						go g.onPeerError(&ErrPeer{err, peer})
					}
					// Always sleep at least the err duration
					time.Sleep(g.peerSleepOnError)
				} else {
					// Go over each message and see if it needs delivering or rebroadcasting
					for _, msg := range msgs {
						rMsg := &MessageReceived{Message: msg, peer: peer}
						if msg.Ack != "" && len(msg.Put) > 0 {
							g.messageIDPutListenersLock.RLock()
							l := g.messageIDPutListeners[msg.Ack]
							g.messageIDPutListenersLock.RUnlock()
							if l != nil {
								go safeReceivedMessageSend(l, rMsg)
								continue
							}
						}
						go g.onUnhandledMessage(rMsg)
					}
				}
			}
		}(peer)
	}
}

func (g *Gun) onUnhandledMessage(msg *MessageReceived) {
	// Unhandled message means rebroadcast
	// TODO: we need a timeout or global context here...
	g.send(context.TODO(), msg.Message, msg.peer)
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
