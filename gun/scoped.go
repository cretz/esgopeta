package gun

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

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

var ErrNotObject = errors.New("Scoped value not an object")
var ErrLookupOnTopLevel = errors.New("Cannot do put/lookup on top level")

// Empty string if doesn't exist, ErrNotObject if self or parent not an object
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

func (s *Scoped) Scoped(ctx context.Context, key string, children ...string) *Scoped {
	ret := newScoped(s.gun, s, key)
	for _, child := range children {
		ret = newScoped(s.gun, ret, child)
	}
	return ret
}
