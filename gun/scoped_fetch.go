package gun

import (
	"context"
	"fmt"
)

func (s *Scoped) FetchOne(ctx context.Context) *FetchResult {
	// Try local before remote
	if r := s.FetchOneLocal(ctx); r.Err != nil || r.ValueExists {
		return r
	}
	return s.FetchOneRemote(ctx)
}

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

func (s *Scoped) FetchOneRemote(ctx context.Context) *FetchResult {
	if s.parent == nil {
		return &FetchResult{Err: ErrLookupOnTopLevel, Field: s.field}
	}
	ch := s.FetchRemote(ctx)
	defer s.FetchDone(ch)
	return <-ch
}

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

func (s *Scoped) FetchRemote(ctx context.Context) <-chan *FetchResult {
	ch := make(chan *FetchResult, 1)
	if s.parent == nil {
		ch <- &FetchResult{Err: ErrLookupOnTopLevel, Field: s.field}
		close(ch)
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
	msgCh := make(chan *MessageReceived)
	s.fetchResultListenersLock.Lock()
	s.fetchResultListeners[ch] = &fetchResultListener{req.ID, ch, msgCh}
	s.fetchResultListenersLock.Unlock()
	// Listen for responses to this get
	s.gun.registerMessageIDListener(req.ID, msgCh)
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
				r := &FetchResult{Field: s.field, Peer: msg.Peer}
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
							confRes, r.Err = s.gun.storage.Put(ctx, parentSoul, s.field, newVal, newState, false)
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

func (s *Scoped) FetchDone(ch <-chan *FetchResult) bool {
	s.fetchResultListenersLock.Lock()
	l := s.fetchResultListeners[ch]
	delete(s.fetchResultListeners, ch)
	s.fetchResultListenersLock.Unlock()
	if l != nil {
		// Unregister the chan
		s.gun.unregisterMessageIDListener(l.id)
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

type fetchResultListener struct {
	id               string
	results          chan *FetchResult
	receivedMessages chan *MessageReceived
}

type FetchResult struct {
	// This can be a context error on cancelation
	Err   error
	Field string
	// Nil if the value doesn't exist, exists and is nil, or there's an error
	Value       Value
	State       State // This can be 0 for errors or top-level value relations
	ValueExists bool
	// Nil when local and sometimes on error
	Peer *Peer
}
