package gun

import (
	"context"
	"fmt"
)

type putResultListener struct {
	id               string
	results          chan *PutResult
	receivedMessages chan *messageReceived
}

// PutResult is either an acknowledgement or an error for a put.
type PutResult struct {
	// Err is any error on put, local or remote. This can be a context error
	// if the put's context completes. This is nil on successful put.
	//
	// This may be ErrLookupOnTopLevel for a remote fetch of a top-level field.
	// This may be ErrNotObject if the field is a child of a non-relation value.
	Err error
	// Field is the name of the field that was put. It is a convenience value
	// for the scope's field this was originally called on.
	Field string
	// Peer is the peer this result is for. This is nil for results from local
	// storage. This may be nil on error.
	Peer *Peer
}

// PutOption is the base interface for all options that can be passed to Put.
type PutOption interface {
	putOption()
}

type putOptionStoreLocalOnly struct{}

func (putOptionStoreLocalOnly) putOption() {}

// PutOptionStoreLocalOnly makes Put only store locally and then be done.
var PutOptionStoreLocalOnly PutOption = putOptionStoreLocalOnly{}

type putOptionFailWithoutParent struct{}

func (putOptionFailWithoutParent) putOption() {}

// PutOptionFailWithoutParent makes Put fail if it would need to lazily create
// parent relations.
var PutOptionFailWithoutParent PutOption = putOptionFailWithoutParent{}

// Put puts a value on the field in local storage. It also sends the put to all
// peers unless the PutOptionStoreLocalOnly option is present. Each
// acknowledgement or error will be sent to the resulting channel. Unless the
// PutOptionFailWithoutParent option is present, this will lazily create all
// parent relations that do not already exist. This will error if called for a
// top-level field. The resulting channel is closed on Gun close, context
// completion, or when PutDone is called with it. Users should ensure one of the
// three happen in a reasonable timeframe to stop listening for acks and prevent
// leaks.
//
// This is the equivalent of the Gun JS API "put" function.
func (s *Scoped) Put(ctx context.Context, val Value, opts ...PutOption) <-chan *PutResult {
	// Collect the options
	storeLocalOnly := false
	failWithoutParent := false
	for _, opt := range opts {
		switch opt.(type) {
		case putOptionStoreLocalOnly:
			storeLocalOnly = true
		case putOptionFailWithoutParent:
			failWithoutParent = true
		}
	}
	ch := make(chan *PutResult, 1)
	// Get all the parents
	parents := []*Scoped{}
	for next := s.parent; next != nil; next = next.parent {
		parents = append([]*Scoped{next}, parents...)
	}
	if len(parents) == 0 {
		ch <- &PutResult{Err: ErrLookupOnTopLevel}
		return ch
	}
	// Ask for the soul on the last parent. What this will do is trigger
	// lazy soul fetch up the chain. Then we can go through and find who doesn't have a
	// cached soul, create one, and store locally.
	if soul, err := parents[len(parents)-1].Soul(ctx); err != nil {
		ch <- &PutResult{Err: ErrLookupOnTopLevel, Field: s.field}
		return ch
	} else if soul == "" && failWithoutParent {
		ch <- &PutResult{Err: fmt.Errorf("Parent not present but required"), Field: s.field}
		return ch
	}
	// Now for every parent that doesn't have a cached soul we create one and
	// put as part of the message. We accept fetching the cache this way is a bit
	// racy.
	req := &Message{
		ID:  randString(9),
		Put: make(map[string]*Node),
	}
	// We know that the first has a soul
	prevParentSoul := parents[0].cachedSoul()
	currState := StateNow()
	for _, parent := range parents[1:] {
		parentCachedSoul := parent.cachedSoul()
		if parentCachedSoul == "" {
			// Create the soul and make it as part of the next put
			parentCachedSoul = s.gun.soulGen()
			req.Put[prevParentSoul] = &Node{
				Metadata: Metadata{
					Soul:  prevParentSoul,
					State: map[string]State{parent.field: currState},
				},
				Values: map[string]Value{parent.field: ValueRelation(parentCachedSoul)},
			}
			// Also store locally and set the cached soul
			// TODO: Should I not store until the very end just in case it errors halfway
			// though? There are no standard cases where it should fail.
			if _, err := s.gun.storage.Put(ctx, prevParentSoul, parent.field, ValueRelation(parentCachedSoul), currState, false); err != nil {
				ch <- &PutResult{Err: err, Field: s.field}
				return ch
			} else if !parent.setCachedSoul(ValueRelation(parentCachedSoul)) {
				ch <- &PutResult{Err: fmt.Errorf("Concurrent cached soul set"), Field: s.field}
				return ch
			}
		}
		prevParentSoul = parentCachedSoul
	}
	// Now that we've setup all the parents, we can do this store locally
	if _, err := s.gun.storage.Put(ctx, prevParentSoul, s.field, val, currState, false); err != nil {
		ch <- &PutResult{Err: err, Field: s.field}
		return ch
	}
	// We need an ack for local store and stop if local only
	ch <- &PutResult{Field: s.field}
	if storeLocalOnly {
		return ch
	}
	// Now, we begin the remote storing
	req.Put[prevParentSoul] = &Node{
		Metadata: Metadata{
			Soul:  prevParentSoul,
			State: map[string]State{s.field: currState},
		},
		Values: map[string]Value{s.field: val},
	}
	// Make a msg chan and register it to listen for acks
	msgCh := make(chan *messageReceived)
	s.putResultListenersLock.Lock()
	s.putResultListeners[ch] = &putResultListener{req.ID, ch, msgCh}
	s.putResultListenersLock.Unlock()
	s.gun.registerMessageIDListener(req.ID, msgCh)
	// Start message listener
	go func() {
		for {
			select {
			case <-ctx.Done():
				ch <- &PutResult{Err: ctx.Err(), Field: s.field}
				s.PutDone(ch)
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				r := &PutResult{Field: s.field, Peer: msg.peer}
				if msg.Err != "" {
					r.Err = fmt.Errorf("Remote error: %v", msg.Err)
				} else if msg.OK != 1 {
					r.Err = fmt.Errorf("Unexpected remote ok value of %v", msg.OK)
				}
				safePutResultSend(ch, r)
			}
		}
	}()
	// Send async, sending back errors
	go func() {
		for peerErr := range s.gun.send(ctx, req, nil) {
			safePutResultSend(ch, &PutResult{
				Err:   peerErr.Err,
				Field: s.field,
				Peer:  peerErr.Peer,
			})
		}
	}()
	return ch
}

// PutDone is called with a channel returned from Put to stop
// listening for acks and close the channel. It returns true if it actually
// stopped listening or false if it wasn't listening.
func (s *Scoped) PutDone(ch <-chan *PutResult) bool {
	s.putResultListenersLock.Lock()
	l := s.putResultListeners[ch]
	delete(s.putResultListeners, ch)
	s.putResultListenersLock.Unlock()
	if l != nil {
		// Unregister the chan
		s.gun.unregisterMessageIDListener(l.id)
		// Close the message chan and the result chan
		close(l.receivedMessages)
		close(l.results)
	}
	return l != nil
}

func safePutResultSend(ch chan<- *PutResult, r *PutResult) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- r
}
