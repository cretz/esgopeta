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
	myPeerID         string
	tracking         Tracking

	messageIDListeners     map[string]chan<- *MessageReceived
	messageIDListenersLock sync.RWMutex
}

type Config struct {
	PeerURLs         []string
	Storage          Storage
	SoulGen          func() string
	PeerErrorHandler func(*ErrPeer)
	PeerSleepOnError time.Duration
	MyPeerID         string
	Tracking         Tracking
}

type Tracking int

const (
	TrackingRequested Tracking = iota
	TrackingNothing
	TrackingEverything
)

const DefaultPeerSleepOnError = 30 * time.Second

func New(ctx context.Context, config Config) (*Gun, error) {
	g := &Gun{
		peers:              make([]*gunPeer, len(config.PeerURLs)),
		storage:            config.Storage,
		soulGen:            config.SoulGen,
		peerErrorHandler:   config.PeerErrorHandler,
		peerSleepOnError:   config.PeerSleepOnError,
		myPeerID:           config.MyPeerID,
		tracking:           config.Tracking,
		messageIDListeners: map[string]chan<- *MessageReceived{},
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
		if g.peers[i], err = newGunPeer(peerURL, connPeer, sleepOnError); err != nil {
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
		g.soulGen = DefaultSoulGen
	}
	if g.myPeerID == "" {
		g.myPeerID = randString(9)
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
			for !peer.closed() {
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
						g.onPeerMessage(ctx, &MessageReceived{Message: msg, peer: peer})
					}
				}
			}
		}(peer)
	}
}

func (g *Gun) onPeerMessage(ctx context.Context, msg *MessageReceived) {
	// If there is a listener for this message, use it
	if msg.Ack != "" {
		g.messageIDListenersLock.RLock()
		l := g.messageIDListeners[msg.Ack]
		g.messageIDListenersLock.RUnlock()
		if l != nil {
			go safeReceivedMessageSend(l, msg)
			return
		}
	}
	// DAM messages are either requests for our ID or setting of theirs
	if msg.DAM != "" {
		if msg.PID == "" {
			// This is a request, set the PID and send it back
			msg.PID = g.myPeerID
			if _, err := msg.peer.send(ctx, msg.Message); err != nil {
				go g.onPeerError(&ErrPeer{err, msg.peer})
			}
		} else {
			// This is them telling us theirs
			msg.peer.id = msg.PID
		}
		return
	}
	// Unhandled message means rebroadcast
	g.send(ctx, msg.Message, msg.peer)
}

func (g *Gun) onPeerError(err *ErrPeer) {
	if g.peerErrorHandler != nil {
		g.peerErrorHandler(err)
	}
}

func (g *Gun) RegisterMessageIDListener(id string, ch chan<- *MessageReceived) {
	g.messageIDListenersLock.Lock()
	defer g.messageIDListenersLock.Unlock()
	g.messageIDListeners[id] = ch
}

func (g *Gun) UnregisterMessageIDListener(id string) {
	g.messageIDListenersLock.Lock()
	defer g.messageIDListenersLock.Unlock()
	delete(g.messageIDListeners, id)
}

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
