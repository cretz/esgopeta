package gun

import (
	"context"
	"fmt"
)

type fetchResultListener struct {
	id               string
	parentSoul       string
	results          chan *FetchResult
	receivedMessages chan *messageReceived
}

// FetchResult is a result of a fetch.
type FetchResult struct {
	// Err is any error on fetch, local or remote. This can be a context error
	// if the fetch's context completes. This is nil on successful fetch.
	//
	// This may be ErrLookupOnTopLevel for a remote fetch of a top-level field.
	// This may be ErrNotObject if the field is a child of a non-relation value.
	Err error
	// Field is the name of the field that was fetched. It is a convenience
	// value for the scope's field this was originally called on.
	Field string
	// Value is the fetched value. This will be nil if Err is not nil. This will
	// also be nil if a peer said the value does not exist. This will also be
	// nil if the value exists but is nil. Use ValueExists to distinguish
	// between the last two cases.
	Value Value
	// State is the conflict state of the value. It may be 0 for errors. It may
	// also be 0 if this is a top-level field.
	State State
	// ValueExists is true if there is no error and the fetched value, nil or
	// not, does exist.
	ValueExists bool
	// Peer is the peer this result is for. This is nil for results from local
	// storage. This may be nil on error.
	Peer *Peer
}

// FetchOne fetches a single result, trying local first. It is a shortcut for
// calling FetchOneLocal and if it doesn't exist falling back to FetchOneRemote.
// This should not be called on a top-level field. The context can be used to
// timeout the wait.
//
// This is the equivalent of the Gun JS API "once" function.
func (s *Scoped) FetchOne(ctx context.Context) *FetchResult {
	// Try local before remote
	if r := s.FetchOneLocal(ctx); r.Err != nil || r.ValueExists {
		return r
	}
	return s.FetchOneRemote(ctx)
}

// FetchOneLocal gets a local value from storage. For top-level fields, it
// simply returns the field name as a relation.
func (s *Scoped) FetchOneLocal(ctx context.Context) *FetchResult {
	// If there is no parent, this is just the relation
	if s.parent == nil {
		return &FetchResult{Field: s.field, Value: ValueRelation(s.field), ValueExists: true}
	}
	r := &FetchResult{Field: s.field}
	// Need parent soul for lookup
	var parentSoul string
	if parentSoul, r.Err = s.parent.Soul(ctx); r.Err == nil {
		if r.Value, r.State, r.Err = s.gun.storage.Get(ctx, parentSoul, s.field); r.Err == ErrStorageNotFound {
			r.Err = nil
		} else if r.Err == nil {
			r.ValueExists = true
		}
	}
	return r
}

// FetchOneRemote fetches a single result from the first peer that responds. It
// will not look in storage first. This will error if called for a top-level
// field. This is a shortcut for calling FetchRemote, waiting for a single
// value, then calling FetchDone. The context can be used to timeout the wait.
func (s *Scoped) FetchOneRemote(ctx context.Context) *FetchResult {
	if s.parent == nil {
		return &FetchResult{Err: ErrLookupOnTopLevel, Field: s.field}
	}
	ch := s.FetchRemote(ctx)
	defer s.FetchDone(ch)
	return <-ch
}

// Fetch fetches and listens for updates on the field and sends them to the
// resulting channel. This will error if called for a top-level field. The
// resulting channel is closed on Gun close, context completion, or when
// FetchDone is called with it. Users should ensure one of the three happen in a
// reasonable timeframe to stop listening and prevent leaks. This is a shortcut
// for FetchOneLocal (but doesn't send to channel if doesn't exist) followed by
// FetchRemote.
//
// This is the equivalent of the Gun JS API "on" function.
func (s *Scoped) Fetch(ctx context.Context) <-chan *FetchResult {
	ch := make(chan *FetchResult, 1)
	if s.parent == nil {
		ch <- &FetchResult{Err: ErrLookupOnTopLevel, Field: s.field}
		close(ch)
	} else {
		if r := s.FetchOneLocal(ctx); r.Err != nil || r.ValueExists {
			ch <- r
		}
		go s.fetchRemote(ctx, ch)
	}
	return ch
}

// FetchRemote fetches and listens for updates on a field only from peers, not
// via storage. This will error if called for a top-level field. The resulting
// channel is closed on Gun close, context completion, or when FetchDone is
// called with it. Users should ensure one of the three happen in a reasonable
// timeframe to stop listening and prevent leaks.
func (s *Scoped) FetchRemote(ctx context.Context) <-chan *FetchResult {
	ch := make(chan *FetchResult, 1)
	if s.parent == nil {
		ch <- &FetchResult{Err: ErrLookupOnTopLevel, Field: s.field}
	} else {
		go s.fetchRemote(ctx, ch)
	}
	return ch
}

func (s *Scoped) fetchRemote(ctx context.Context, ch chan *FetchResult) {
	if s.parent == nil {
		panic("No parent")
	}
	// We have to get the parent soul first
	parentSoul, err := s.parent.Soul(ctx)
	if err != nil {
		ch <- &FetchResult{Err: ErrLookupOnTopLevel, Field: s.field}
		return
	}
	// Create get request
	req := &Message{
		ID:  randString(9),
		Get: &MessageGetRequest{Soul: parentSoul, Field: s.field},
	}
	// Make a chan to listen for received messages and link it to
	// the given one so we can turn it "off". Off will close this
	// chan.
	msgCh := make(chan *messageReceived)
	s.fetchResultListenersLock.Lock()
	s.fetchResultListeners[ch] = &fetchResultListener{req.ID, parentSoul, ch, msgCh}
	s.fetchResultListenersLock.Unlock()
	// Listen for responses to this get
	s.gun.registerMessageIDListener(req.ID, msgCh)
	s.gun.registerMessageSoulListener(parentSoul, msgCh)
	// TODO: Also listen for any changes to the value or just for specific requests?
	// Handle received messages turning them to value fetches
	var lastSeenValue Value
	var lastSeenState State
	go func() {
		for {
			select {
			case <-ctx.Done():
				ch <- &FetchResult{Err: ctx.Err(), Field: s.field}
				s.FetchDone(ch)
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				r := &FetchResult{Field: s.field, Peer: msg.peer}
				// We asked for a single field, should only get that field or it doesn't exist
				if msg.Err != "" {
					r.Err = fmt.Errorf("Remote error: %v", msg.Err)
				} else if n := msg.Put[parentSoul]; n != nil {
					if newVal, ok := n.Values[s.field]; ok {
						newState := n.State[s.field]
						// Dedupe the value
						if lastSeenValue == newVal && lastSeenState == newState {
							continue
						}
						// If we're storing only what we requested (we do "everything" at a higher level), do it here
						// and only send result if it was an update. Otherwise only do it if we would have done one.
						confRes := ConflictResolutionNeverSeenUpdate
						if s.gun.tracking == TrackingRequested {
							// Wait, wait, we may have already stored this
							alreadyStored := false
							for _, storedField := range msg.storedPuts[parentSoul] {
								if storedField == s.field {
									alreadyStored = true
									break
								}
							}
							if !alreadyStored {
								confRes, r.Err = s.gun.storage.Put(ctx, parentSoul, s.field, newVal, newState, false)
							}
						} else if lastSeenState > 0 {
							confRes = ConflictResolve(lastSeenValue, lastSeenState, newVal, newState, StateNow())
						}
						// If there are no errors and it was an update, update the last seen and set the response vals
						if r.Err == nil && confRes.IsImmediateUpdate() {
							lastSeenValue, lastSeenState = newVal, newState
							r.Value, r.State, r.ValueExists = newVal, newState, true
						}
					}
				}
				safeFetchResultSend(ch, r)
			}
		}
	}()
	// Send async, sending back errors
	go func() {
		for peerErr := range s.gun.send(ctx, req, nil) {
			safeFetchResultSend(ch, &FetchResult{
				Err:   peerErr.Err,
				Field: s.field,
				Peer:  peerErr.Peer,
			})
		}
	}()
}

// FetchDone is called with a channel returned from Fetch or FetchRemote to stop
// listening and close the channel. It returns true if it actually stopped
// listening or false if it wasn't listening.
func (s *Scoped) FetchDone(ch <-chan *FetchResult) bool {
	s.fetchResultListenersLock.Lock()
	l := s.fetchResultListeners[ch]
	delete(s.fetchResultListeners, ch)
	s.fetchResultListenersLock.Unlock()
	if l != nil {
		// Unregister the chan
		s.gun.unregisterMessageIDListener(l.id)
		s.gun.unregisterMessageSoulListener(l.parentSoul)
		// Close the message chan and the result chan
		close(l.receivedMessages)
		close(l.results)
	}
	return l != nil
}

func safeFetchResultSend(ch chan<- *FetchResult, r *FetchResult) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- r
}
