package gun

import (
	"context"
	"fmt"
)

type putResultListener struct {
	id               string
	results          chan *PutResult
	receivedMessages chan *MessageReceived
}

type PutResult struct {
	Err error
	// Nil on error or local put success
	Peer *Peer
}

type PutOption interface{}

type putOptionStoreLocalOnly struct{}

func PutOptionStoreLocalOnly() PutOption { return putOptionStoreLocalOnly{} }

type putOptionFailWithoutParent struct{}

func PutOptionFailWithoutParent() PutOption { return putOptionFailWithoutParent{} }

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
		close(ch)
		return ch
	}
	// Ask for the soul on the last parent. What this will do is trigger
	// lazy soul fetch up the chain. Then we can go through and find who doesn't have a
	// cached soul, create one, and store locally.
	if soul, err := parents[len(parents)-1].Soul(ctx); err != nil {
		ch <- &PutResult{Err: ErrLookupOnTopLevel}
		close(ch)
		return ch
	} else if soul == "" && failWithoutParent {
		ch <- &PutResult{Err: fmt.Errorf("Parent not present but required")}
		close(ch)
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
				ch <- &PutResult{Err: err}
				close(ch)
				return ch
			} else if !parent.setCachedSoul(ValueRelation(parentCachedSoul)) {
				ch <- &PutResult{Err: fmt.Errorf("Concurrent cached soul set")}
				close(ch)
				return ch
			}
		}
		prevParentSoul = parentCachedSoul
	}
	// Now that we've setup all the parents, we can do this store locally
	if _, err := s.gun.storage.Put(ctx, prevParentSoul, s.field, val, currState, false); err != nil {
		ch <- &PutResult{Err: err}
		close(ch)
		return ch
	}
	// We need an ack for local store and stop if local only
	ch <- &PutResult{}
	if storeLocalOnly {
		close(ch)
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
	msgCh := make(chan *MessageReceived)
	s.putResultListenersLock.Lock()
	s.putResultListeners[ch] = &putResultListener{req.ID, ch, msgCh}
	s.putResultListenersLock.Unlock()
	s.gun.registerMessageIDListener(req.ID, msgCh)
	// Start message listener
	go func() {
		for {
			select {
			case <-ctx.Done():
				ch <- &PutResult{Err: ctx.Err()}
				s.PutDone(ch)
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				r := &PutResult{Peer: msg.Peer}
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
				Err:  peerErr.Err,
				Peer: peerErr.Peer,
			})
		}
	}()
	return ch
}

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
