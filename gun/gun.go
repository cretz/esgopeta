package gun

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Gun is the main client/server instance for the database.
type Gun struct {
	// currentPeers is the up-to-date peer list. Do not access this without
	// using the associated lock. This slice is never mutated, only replaced.
	// There are methods to get and change the list.
	currentPeers     []*Peer
	currentPeersLock sync.RWMutex

	storage          Storage
	soulGen          func() string
	peerErrorHandler func(*ErrPeer)
	peerSleepOnError time.Duration
	myPeerID         string
	tracking         Tracking

	serversCancelFn context.CancelFunc

	messageIDListeners     map[string]chan<- *messageReceived
	messageIDListenersLock sync.RWMutex

	messageSoulListeners     map[string]chan<- *messageReceived
	messageSoulListenersLock sync.RWMutex
}

// Config is the configuration for a Gun instance.
type Config struct {
	// PeerURLs is the initial set of peer URLs to connect to. This can be empty
	// to not connect to any peers.
	PeerURLs []string
	// Servers is the set of local servers to listen for new peers on. This can
	// be empty to not run any server.
	Servers []Server
	// Storage is the backend storage to locally store data. If not present, a
	// NewStorageInMem is created with a value expiration of
	// DefaultOldestAllowedStorageValue.
	Storage Storage
	// SoulGen is a generator for new soul values. If not present,
	// DefaultSoulGen is used.
	SoulGen func() string
	// PeerErrorHandler is called on every error from or concerning a peer. Not
	// required.
	PeerErrorHandler func(*ErrPeer)
	// PeerSleepOnError is the amount of time we will consider a reconnectable
	// peer "bad" on error before attempting reconnect. If 0 (i.e. not set),
	// DefaultPeerSleepOnError is used.
	PeerSleepOnError time.Duration
	// MyPeerID is the identifier given for this peer to whoever asks. If empty
	// it is set to a random string
	MyPeerID string
	// Tracking is how seen values are updated when they are seen on the wire.
	// When set to TrackingNothing, no seen values will be updated. When set to
	// TrackingRequested (the default), only values that are already in storage
	// will be updated when seen. When set to TrackingEverything, all values
	// seen on the wire will be stored.
	Tracking Tracking
}

// Tracking is what to do when a value update is seen on the wire.
type Tracking int

const (
	// TrackingRequested means any value we have already stored will be updated
	// when seen.
	TrackingRequested Tracking = iota
	// TrackingNothing means no values will be updated.
	TrackingNothing
	// TrackingEverything means every value update is stored.
	TrackingEverything
)

// DefaultPeerSleepOnError is the default amount of time that an errored
// reconnectable peer will wait until trying to reconnect.
const DefaultPeerSleepOnError = 30 * time.Second

// DefaultOldestAllowedStorageValue is the default NewStorageInMem expiration.
const DefaultOldestAllowedStorageValue = 7 * (60 * time.Minute)

// New creates a new Gun instance for the given config. The given context is
// used for all peer connection reconnects now/later, all server servings, and
// peer message handling. Therefore it should be kept available at least until
// Close. Callers must still call Close on complete, the completion of the
// context does not automatically do it.
//
// This returns nil with an error on any initial peer connection failure (and
// cleans up any other peers temporarily connected to).
func New(ctx context.Context, config Config) (*Gun, error) {
	g := &Gun{
		currentPeers:         make([]*Peer, len(config.PeerURLs)),
		storage:              config.Storage,
		soulGen:              config.SoulGen,
		peerErrorHandler:     config.PeerErrorHandler,
		peerSleepOnError:     config.PeerSleepOnError,
		myPeerID:             config.MyPeerID,
		tracking:             config.Tracking,
		messageIDListeners:   map[string]chan<- *messageReceived{},
		messageSoulListeners: map[string]chan<- *messageReceived{},
	}
	// Set config defaults
	if g.storage == nil {
		g.storage = NewStorageInMem(DefaultOldestAllowedStorageValue)
	}
	if g.soulGen == nil {
		g.soulGen = DefaultSoulGen
	}
	if g.peerSleepOnError == 0 {
		g.peerSleepOnError = DefaultPeerSleepOnError
	}
	if g.myPeerID == "" {
		g.myPeerID = randString(9)
	}
	// Create all the peers
	var err error
	for i := 0; i < len(config.PeerURLs) && err == nil; i++ {
		peerURL := config.PeerURLs[i]
		newConn := func() (PeerConn, error) { return NewPeerConn(ctx, peerURL) }
		if g.currentPeers[i], err = newPeer(peerURL, newConn, g.peerSleepOnError); err != nil {
			err = fmt.Errorf("Failed connecting to peer %v: %v", peerURL, err)
		}
	}
	// If there was an error, we need to close what we did create
	if err != nil {
		for _, peer := range g.currentPeers {
			if peer != nil {
				peer.Close()
			}
		}
		return nil, err
	}
	// Start receiving from peers
	for _, peer := range g.currentPeers {
		go g.startReceiving(ctx, peer)
	}
	// Start all the servers
	go g.startServers(ctx, config.Servers)
	return g, nil
}

// Scoped returns a scoped instance to the given field and children for reading
// and writing. This is the equivalent of calling the Gun JS API "get" function
// (sans callback) for each field/child.
func (g *Gun) Scoped(ctx context.Context, field string, children ...string) *Scoped {
	s := newScoped(g, nil, field)
	if len(children) > 0 {
		s = s.Scoped(ctx, children[0], children[1:]...)
	}
	return s
}

// Close stops all peers and servers and closes the underlying storage.
func (g *Gun) Close() error {
	var errs []error
	for _, p := range g.peers() {
		if err := p.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	g.serversCancelFn()
	if err := g.storage.Close(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil
	} else if len(errs) == 1 {
		return errs[0]
	} else {
		return fmt.Errorf("Multiple errors: %v", errs)
	}
}

func (g *Gun) peers() []*Peer {
	g.currentPeersLock.RLock()
	defer g.currentPeersLock.RUnlock()
	return g.currentPeers
}

func (g *Gun) addPeer(p *Peer) {
	g.currentPeersLock.Lock()
	defer g.currentPeersLock.Unlock()
	prev := g.currentPeers
	g.currentPeers = make([]*Peer, len(prev)+1)
	copy(g.currentPeers, prev)
	g.currentPeers[len(prev)] = p
}

func (g *Gun) removePeer(p *Peer) {
	g.currentPeersLock.Lock()
	defer g.currentPeersLock.Unlock()
	prev := g.currentPeers
	g.currentPeers = make([]*Peer, 0, len(prev))
	for _, peer := range prev {
		if peer != p {
			g.currentPeers = append(g.currentPeers, peer)
		}
	}
}

func (g *Gun) send(ctx context.Context, msg *Message, ignorePeer *Peer) <-chan *ErrPeer {
	peers := g.peers()
	ch := make(chan *ErrPeer, len(peers))
	// Everything async
	go func() {
		defer close(ch)
		var wg sync.WaitGroup
		for _, peer := range peers {
			if peer == ignorePeer {
				continue
			}
			wg.Add(1)
			go func(peer *Peer) {
				defer wg.Done()
				// Just do nothing if the peer is bad and we couldn't send
				if _, err := peer.send(ctx, msg); err != nil {
					if !peer.reconnectSupported() {
						g.removePeer(peer)
					}
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

func (g *Gun) startReceiving(ctx context.Context, peer *Peer) {
	ctx, cancelFn := context.WithCancel(ctx)
	defer cancelFn()
	for !peer.Closed() && ctx.Err() == nil {
		// We might not be able receive because peer is sleeping from
		// an error happened within or a just-before send error.
		if ok, msgs, err := peer.receive(ctx); !ok {
			if err != nil {
				go g.onPeerError(&ErrPeer{err, peer})
			}
			// If can reconnect, sleep at least the err duration, otherwise remove
			if peer.reconnectSupported() {
				time.Sleep(g.peerSleepOnError)
			} else {
				g.removePeer(peer)
			}
		} else {
			// Go over each message and see if it needs delivering or rebroadcasting
			for _, msg := range msgs {
				g.onPeerMessage(ctx, &messageReceived{Message: msg, peer: peer})
			}
		}
	}
}

func (g *Gun) onPeerMessage(ctx context.Context, msg *messageReceived) {
	// TODO:
	//	* if message-acks are not considered part of a store-all server's storage, then use msg.Ack
	//	  to determine whether we even put here instead of how we do it now.
	//	* handle gets

	// If we're tracking anything, we try to put it (may only be if exists).
	if g.tracking != TrackingNothing {
		// If we're tracking everything, we persist everything. Otherwise if we're
		// only tracking requested, we persist only if it already exists.
		putOnlyIfExists := g.tracking == TrackingRequested
		for parentSoul, node := range msg.Put {
			for field, value := range node.Values {
				if state, ok := node.Metadata.State[field]; ok {
					// TODO: warn on other error or something
					_, err := g.storage.Put(ctx, parentSoul, field, value, state, putOnlyIfExists)
					if err == nil {
						if msg.storedPuts == nil {
							msg.storedPuts = map[string][]string{}
						}
						msg.storedPuts[parentSoul] = append(msg.storedPuts[parentSoul], field)
					}
				}
			}
		}
	}

	// If there is a listener for this message ID, use it and consider the message handled
	if msg.Ack != "" {
		g.messageIDListenersLock.RLock()
		l := g.messageIDListeners[msg.Ack]
		g.messageIDListenersLock.RUnlock()
		if l != nil {
			go safeReceivedMessageSend(l, msg)
			return
		}
	}

	// If there are listeners for any of the souls, use them but don't consider the message handled
	for parentSoul := range msg.Put {
		g.messageSoulListenersLock.RLock()
		l := g.messageSoulListeners[parentSoul]
		g.messageSoulListenersLock.RUnlock()
		if l != nil {
			go safeReceivedMessageSend(l, msg)
		}
	}

	// DAM messages are either requests for our ID or setting of theirs
	if msg.DAM != "" {
		if msg.PID == "" {
			// This is a request, set the PID and send it back
			msg.PID = g.myPeerID
			if _, err := msg.peer.send(ctx, msg.Message); err != nil {
				go g.onPeerError(&ErrPeer{err, msg.peer})
				if !msg.peer.reconnectSupported() {
					g.removePeer(msg.peer)
				}
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

func (g *Gun) registerMessageIDListener(id string, ch chan<- *messageReceived) {
	g.messageIDListenersLock.Lock()
	defer g.messageIDListenersLock.Unlock()
	g.messageIDListeners[id] = ch
}

func (g *Gun) unregisterMessageIDListener(id string) {
	g.messageIDListenersLock.Lock()
	defer g.messageIDListenersLock.Unlock()
	delete(g.messageIDListeners, id)
}

func (g *Gun) registerMessageSoulListener(soul string, ch chan<- *messageReceived) {
	g.messageSoulListenersLock.Lock()
	defer g.messageSoulListenersLock.Unlock()
	g.messageSoulListeners[soul] = ch
}

func (g *Gun) unregisterMessageSoulListener(soul string) {
	g.messageSoulListenersLock.Lock()
	defer g.messageSoulListenersLock.Unlock()
	delete(g.messageSoulListeners, soul)
}

func safeReceivedMessageSend(ch chan<- *messageReceived, msg *messageReceived) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- msg
}
