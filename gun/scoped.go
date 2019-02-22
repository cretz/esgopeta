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

	resultChansToListeners     map[<-chan *Result]*messageIDListener
	resultChansToListenersLock sync.Mutex
}

type messageIDListener struct {
	id               string
	results          chan *Result
	receivedMessages chan *MessageReceived
}

func newScoped(gun *Gun, parent *Scoped, field string) *Scoped {
	return &Scoped{
		gun:                    gun,
		parent:                 parent,
		field:                  field,
		resultChansToListeners: map[<-chan *Result]*messageIDListener{},
	}
}

type Result struct {
	// This can be a context error on cancelation
	Err   error
	Field string
	// Nil if the value doesn't exist, exists and is nil, or there's an error
	Value       Value
	State       int64 // This can be 0 for errors or top-level value relations
	ValueExists bool
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
	} else if r := s.Val(ctx); r.Err != nil {
		return "", r.Err
	} else if !r.ValueExists {
		return "", nil
	} else if rel, ok := r.Value.(ValueRelation); !ok {
		return "", ErrNotObject
	} else {
		s.cachedParentSoulLock.Lock()
		s.cachedParentSoul = string(rel)
		s.cachedParentSoulLock.Unlock()
		return string(rel), nil
	}
}

func (s *Scoped) Put(ctx context.Context, val Value) <-chan *Result {
	panic("TODO")
}

func (s *Scoped) Val(ctx context.Context) *Result {
	// Try local before remote
	if r := s.ValLocal(ctx); r.Err != nil || r.ValueExists {
		return r
	}
	return s.ValRemote(ctx)
}

func (s *Scoped) ValLocal(ctx context.Context) *Result {
	// If there is no parent, this is just the relation
	if s.parent == nil {
		return &Result{Field: s.field, Value: ValueRelation(s.field), ValueExists: true}
	}
	r := &Result{Field: s.field}
	// Need parent soul for lookup
	var parentSoul string
	if parentSoul, r.Err = s.parent.Soul(ctx); r.Err == nil {
		var vs *ValueWithState
		if vs, r.Err = s.gun.storage.Get(ctx, parentSoul, s.field); r.Err == ErrStorageNotFound {
			r.Err = nil
		} else if r.Err == nil {
			r.Value, r.State, r.ValueExists = vs.Value, vs.State, true
		}
	}
	return r
}

func (s *Scoped) ValRemote(ctx context.Context) *Result {
	if s.parent == nil {
		return &Result{Err: ErrLookupOnTopLevel, Field: s.field}
	}
	ch := s.OnRemote(ctx)
	defer s.Off(ch)
	return <-ch
}

func (s *Scoped) On(ctx context.Context) <-chan *Result {
	ch := make(chan *Result, 1)
	if s.parent == nil {
		ch <- &Result{Err: ErrLookupOnTopLevel, Field: s.field}
		close(ch)
	} else {
		if r := s.ValLocal(ctx); r.Err != nil || r.ValueExists {
			ch <- r
		}
		go s.onRemote(ctx, ch)
	}
	return ch
}

func (s *Scoped) OnRemote(ctx context.Context) <-chan *Result {
	ch := make(chan *Result, 1)
	if s.parent == nil {
		ch <- &Result{Err: ErrLookupOnTopLevel, Field: s.field}
		close(ch)
	} else {
		go s.onRemote(ctx, ch)
	}
	return ch
}

func (s *Scoped) onRemote(ctx context.Context, ch chan *Result) {
	if s.parent == nil {
		panic("No parent")
	}
	// We have to get the parent soul first
	parentSoul, err := s.parent.Soul(ctx)
	if err != nil {
		ch <- &Result{Err: ErrLookupOnTopLevel, Field: s.field}
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
	s.resultChansToListenersLock.Lock()
	s.resultChansToListeners[ch] = &messageIDListener{req.ID, ch, msgCh}
	s.resultChansToListenersLock.Unlock()
	// Listen for responses to this get
	s.gun.RegisterMessageIDPutListener(req.ID, msgCh)
	// TODO: only for children: s.gun.RegisterValueIDPutListener(s.id, msgCh)
	// Handle received messages turning them to value fetches
	go func() {
		for {
			select {
			case <-ctx.Done():
				ch <- &Result{Err: ctx.Err(), Field: s.field}
				s.Off(ch)
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				r := &Result{Field: s.field, peer: msg.peer}
				// We asked for a single field, should only get that field or it doesn't exist
				if n := msg.Put[parentSoul]; n != nil && n.Values[s.field] != nil {
					r.Value, r.State, r.ValueExists = n.Values[s.field], n.State[s.field], true
				}
				// TODO: conflict resolution and defer
				// TODO: dedupe
				// TODO: store and cache
				safeResultSend(ch, r)
			}
		}
	}()
	// Send async, sending back errors
	go func() {
		for peerErr := range s.gun.Send(ctx, req) {
			safeResultSend(ch, &Result{
				Err:   peerErr.Err,
				Field: s.field,
				peer:  peerErr.peer,
			})
		}
	}()
}

func (s *Scoped) Off(ch <-chan *Result) bool {
	s.resultChansToListenersLock.Lock()
	l := s.resultChansToListeners[ch]
	delete(s.resultChansToListeners, ch)
	s.resultChansToListenersLock.Unlock()
	if l != nil {
		// Unregister the chan
		s.gun.UnregisterMessageIDPutListener(l.id)
		// Close the message chan and the result chan
		close(l.receivedMessages)
		close(l.results)
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

func safeResultSend(ch chan<- *Result, r *Result) {
	// Due to the fact that we may send on a closed channel here, we ignore the panic
	defer func() { recover() }()
	ch <- r
}
