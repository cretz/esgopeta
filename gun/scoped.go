package gun

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Scoped is a contextual, namespaced field to read or write.
type Scoped struct {
	gun *Gun

	parent               *Scoped
	field                string
	cachedParentSoul     string
	cachedParentSoulLock sync.RWMutex

	fetchResultListeners     map[<-chan *FetchResult]*fetchResultListener
	fetchResultListenersLock sync.Mutex

	putResultListeners     map[<-chan *PutResult]*putResultListener
	putResultListenersLock sync.Mutex
}

func newScoped(gun *Gun, parent *Scoped, field string) *Scoped {
	return &Scoped{
		gun:                  gun,
		parent:               parent,
		field:                field,
		fetchResultListeners: map[<-chan *FetchResult]*fetchResultListener{},
		putResultListeners:   map[<-chan *PutResult]*putResultListener{},
	}
}

// ErrNotObject occurs when a put or a fetch is attempted as a child of an
// existing, non-relation value.
var ErrNotObject = errors.New("Scoped value not an object")

// ErrLookupOnTopLevel occurs when a put or remote fetch is attempted on
// a top-level field.
var ErrLookupOnTopLevel = errors.New("Cannot do put/lookup on top level")

// Soul returns the current soul of this value relation. It returns a cached
// value if called before. Otherwise, it does a FetchOne to get the value
// and return its soul if a relation. If any parent is not a relation or this
// value exists and is not a relation, ErrNotObject is returned. If this field
// doesn't exist yet, an empty string is returned with no error. Otherwise,
// the soul of the relation is returned. The context can be used to timeout the
// fetch.
func (s *Scoped) Soul(ctx context.Context) (string, error) {
	if cachedSoul := s.cachedSoul(); cachedSoul != "" {
		return cachedSoul, nil
	} else if r := s.FetchOne(ctx); r.Err != nil {
		return "", r.Err
	} else if !r.ValueExists {
		return "", nil
	} else if rel, ok := r.Value.(ValueRelation); !ok {
		return "", ErrNotObject
	} else if !s.setCachedSoul(rel) {
		return "", fmt.Errorf("Concurrent soul cache set")
	} else {
		return string(rel), nil
	}
}

func (s *Scoped) cachedSoul() string {
	s.cachedParentSoulLock.RLock()
	defer s.cachedParentSoulLock.RUnlock()
	return s.cachedParentSoul
}

func (s *Scoped) setCachedSoul(val ValueRelation) bool {
	s.cachedParentSoulLock.Lock()
	defer s.cachedParentSoulLock.Unlock()
	if s.cachedParentSoul != "" {
		return false
	}
	s.cachedParentSoul = string(val)
	return true
}

// Scoped returns a scoped instance to the given field and children for reading
// and writing. This is the equivalent of calling the Gun JS API "get" function
// (sans callback) for each field/child.
func (s *Scoped) Scoped(ctx context.Context, field string, children ...string) *Scoped {
	ret := newScoped(s.gun, s, field)
	for _, child := range children {
		ret = newScoped(s.gun, ret, child)
	}
	return ret
}
