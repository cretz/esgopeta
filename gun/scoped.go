package gun

import (
	"context"
	"sync"
)

type Scoped struct {
	gun      *Gun
	parentID string
	field    string

	valueChansToListeners     map[<-chan *ValueFetch]*messageIDListener
	valueChansToListenersLock sync.Mutex
}

type messageIDListener struct {
	id               string
	values           chan *ValueFetch
	receivedMessages chan *ReceivedMessage
}

func newScoped(gun *Gun, parentID string, field string) *Scoped {
	return &Scoped{
		gun:      gun,
		parentID: parentID,
		field:    field,
	}
}

type ValueFetch struct {
	// This can be a context error on cancelation
	Err   error
	Field string
	// Nil if there is an error
	Value *StatefulValue
	// Nil when local and sometimes on error
	Peer Peer
}

type Ack struct {
	Err  error
	Ok   bool
	Peer Peer
}

func (s *Scoped) Val(ctx context.Context) *ValueFetch {
	// Try local before remote
	if v := s.ValLocal(ctx); v != nil {
		return v
	}
	return s.ValRemote(ctx)
}

func (s *Scoped) ValLocal(ctx context.Context) *ValueFetch {
	var v ValueFetch
	if v.Value, v.Err = s.gun.storage.Get(ctx, s.parentID, s.field); v.Err == ErrStorageNotFound {
		return nil
	}
	return &v
}

func (s *Scoped) ValRemote(ctx context.Context) *ValueFetch {
	ch := s.OnRemote(ctx)
	defer s.Off(ch)
	return <-ch
}

func (s *Scoped) On(ctx context.Context) <-chan *ValueFetch {
	ch := make(chan *ValueFetch, 1)
	if v := s.ValLocal(ctx); v != nil {
		ch <- v
	}
	go s.onRemote(ctx, ch)
	return ch
}

func (s *Scoped) OnRemote(ctx context.Context) <-chan *ValueFetch {
	ch := make(chan *ValueFetch, 1)
	go s.onRemote(ctx, ch)
	return ch
}

func (s *Scoped) onRemote(ctx context.Context, ch chan *ValueFetch) {
	// Create get request
	req := &Message{
		ID:  randString(9),
		Get: &MessageGetRequest{ID: s.parentID, Field: s.field},
	}
	// Make a chan to listen for received messages and link it to
	// the given one so we can turn it "off". Off will close this
	// chan.
	msgCh := make(chan *ReceivedMessage)
	s.valueChansToListenersLock.Lock()
	s.valueChansToListeners[ch] = &messageIDListener{req.ID, ch, msgCh}
	s.valueChansToListenersLock.Unlock()
	// Listen for responses to this get
	s.gun.RegisterMessageIDPutListener(req.ID, msgCh)
	// TODO: only for children: s.gun.RegisterValueIDPutListener(s.id, msgCh)
	// Handle received messages turning them to value fetches
	go func() {
		for {
			select {
			case <-ctx.Done():
				ch <- &ValueFetch{Err: ctx.Err(), Field: s.field}
				s.Off(ch)
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				// We asked for a single field, should only get that field
				if n := msg.Put[s.parentID]; n != nil && n.Values[s.field] != nil {
					// TODO: conflict resolution
					// TODO: dedupe
					// TODO: store and cache
					safeValueFetchSend(ch, &ValueFetch{
						Field: s.field,
						Value: &StatefulValue{n.Values[s.field], n.State[s.field]},
						Peer:  msg.Peer,
					})
				}
			}
		}
	}()
	// Send async, sending back errors
	go func() {
		for peerErr := range s.gun.Send(ctx, req) {
			safeValueFetchSend(ch, &ValueFetch{
				Err:   peerErr.Err,
				Field: s.field,
				Peer:  peerErr.Peer,
			})
		}
	}()
}

func (s *Scoped) Off(ch <-chan *ValueFetch) bool {
	s.valueChansToListenersLock.Lock()
	l := s.valueChansToListeners[ch]
	delete(s.valueChansToListeners, ch)
	s.valueChansToListenersLock.Unlock()
	if l != nil {
		// Unregister the chan
		s.gun.UnregisterMessageIDPutListener(l.id)
		// Close the message chan and the value chan
		close(l.receivedMessages)
		close(l.values)
	}
	return l != nil
}

func (s *Scoped) Scoped(ctx context.Context, key string, children ...string) *Scoped {
	panic("TODO")
}

func safeValueFetchSend(ch chan<- *ValueFetch, f *ValueFetch) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- f
}
