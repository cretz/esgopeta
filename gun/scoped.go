package gun

import (
	"context"
	"errors"
	"sync"
)

type Scoped struct {
	gun *Gun

	parent               *Scoped
	field                string
	cachedParentSoul     string
	cachedParentSoulLock sync.RWMutex

	valueChansToListeners     map[<-chan *ValueFetch]*messageIDListener
	valueChansToListenersLock sync.Mutex
}

type messageIDListener struct {
	id               string
	values           chan *ValueFetch
	receivedMessages chan *MessageReceived
}

func newScoped(gun *Gun, parent *Scoped, field string) *Scoped {
	return &Scoped{
		gun:    gun,
		parent: parent,
		field:  field,
	}
}

type ValueFetch struct {
	// This can be a context error on cancelation
	Err   error
	Field string
	// Nil if the value doesn't exist or there's an error
	Value *ValueWithState
	// Nil when local and sometimes on error
	peer *gunPeer
}

var ErrNotObject = errors.New("Scoped value not an object")
var ErrLookupOnTopLevel = errors.New("Cannot do lookup on top level")

// Empty string if doesn't exist, ErrNotObject if self or parent not an object
func (s *Scoped) Soul(ctx context.Context) (string, error) {
	s.cachedParentSoulLock.RLock()
	cachedParentSoul := s.cachedParentSoul
	s.cachedParentSoulLock.RUnlock()
	if cachedParentSoul != "" {
		return cachedParentSoul, nil
	} else if v := s.Val(ctx); v.Err != nil {
		return "", v.Err
	} else if v.Value == nil {
		return "", nil
	} else if rel, ok := v.Value.Value.(ValueRelation); !ok {
		return "", ErrNotObject
	} else {
		s.cachedParentSoulLock.Lock()
		s.cachedParentSoul = string(rel)
		s.cachedParentSoulLock.Unlock()
		return string(rel), nil
	}
}

func (s *Scoped) Val(ctx context.Context) *ValueFetch {
	// Try local before remote
	if v := s.ValLocal(ctx); v != nil {
		return v
	}
	return s.ValRemote(ctx)
}

func (s *Scoped) ValLocal(ctx context.Context) *ValueFetch {
	// If there is no parent, this is just the relation
	if s.parent == nil {
		return &ValueFetch{Field: s.field, Value: &ValueWithState{Value: ValueRelation(s.field)}}
	}
	v := &ValueFetch{Field: s.field}
	// Need parent soul for lookup
	var parentSoul string
	if parentSoul, v.Err = s.parent.Soul(ctx); v.Err == nil {
		if v.Value, v.Err = s.gun.storage.Get(ctx, parentSoul, s.field); v.Err == ErrStorageNotFound {
			return nil
		}
	}
	return v
}

func (s *Scoped) ValRemote(ctx context.Context) *ValueFetch {
	if s.parent == nil {
		return &ValueFetch{Err: ErrLookupOnTopLevel, Field: s.field}
	}
	ch := s.OnRemote(ctx)
	defer s.Off(ch)
	return <-ch
}

func (s *Scoped) On(ctx context.Context) <-chan *ValueFetch {
	ch := make(chan *ValueFetch, 1)
	if s.parent == nil {
		ch <- &ValueFetch{Err: ErrLookupOnTopLevel, Field: s.field}
	} else {
		if v := s.ValLocal(ctx); v != nil {
			ch <- v
		}
		go s.onRemote(ctx, ch)
	}
	return ch
}

func (s *Scoped) OnRemote(ctx context.Context) <-chan *ValueFetch {
	ch := make(chan *ValueFetch, 1)
	if s.parent == nil {
		ch <- &ValueFetch{Err: ErrLookupOnTopLevel, Field: s.field}
	} else {
		go s.onRemote(ctx, ch)
	}
	return ch
}

func (s *Scoped) onRemote(ctx context.Context, ch chan *ValueFetch) {
	if s.parent == nil {
		panic("No parent")
	}
	// We have to get the parent soul first
	parentSoul, err := s.parent.Soul(ctx)
	if err != nil {
		ch <- &ValueFetch{Err: ErrLookupOnTopLevel, Field: s.field}
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
				f := &ValueFetch{Field: s.field, peer: msg.peer}
				// We asked for a single field, should only get that field or it doesn't exist
				if n := msg.Put[parentSoul]; n != nil && n.Values[s.field] != nil {
					f.Value = &ValueWithState{n.Values[s.field], n.State[s.field]}
				}
				// TODO: conflict resolution and defer
				// TODO: dedupe
				// TODO: store and cache
				safeValueFetchSend(ch, f)
			}
		}
	}()
	// Send async, sending back errors
	go func() {
		for peerErr := range s.gun.Send(ctx, req) {
			safeValueFetchSend(ch, &ValueFetch{
				Err:   peerErr.Err,
				Field: s.field,
				peer:  peerErr.peer,
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
	ret := newScoped(s.gun, s, key)
	for _, child := range children {
		ret = newScoped(s.gun, ret, child)
	}
	return ret
}

func safeValueFetchSend(ch chan<- *ValueFetch, f *ValueFetch) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- f
}
