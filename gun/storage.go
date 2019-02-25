package gun

import (
	"context"
	"errors"
	"sync"
)

var ErrStorageNotFound = errors.New("Not found")

type Storage interface {
	Get(ctx context.Context, parentSoul, field string) (Value, State, error)
	// If bool is false, it's deferred
	Put(ctx context.Context, parentSoul, field string, val Value, state State) (bool, error)
	Tracking(ctx context.Context, parentSoul, field string) (bool, error)
}

type StorageInMem struct {
	values sync.Map
}

type parentSoulAndField struct{ parentSoul, field string }

type valueWithState struct {
	val   Value
	state State
}

func (s *StorageInMem) Get(ctx context.Context, parentSoul, field string) (Value, State, error) {
	v, ok := s.values.Load(parentSoulAndField{parentSoul, field})
	if !ok {
		return nil, 0, ErrStorageNotFound
	}
	vs := v.(*valueWithState)
	return vs.val, vs.state, nil
}

func (s *StorageInMem) Put(ctx context.Context, parentSoul, field string, val Value, state State) (bool, error) {
	s.values.Store(parentSoulAndField{parentSoul, field}, &valueWithState{val, state})
	// TODO: conflict resolution state check?
	return true, nil
}

func (s *StorageInMem) Tracking(ctx context.Context, parentSoul, field string) (bool, error) {
	_, ok := s.values.Load(parentSoulAndField{parentSoul, field})
	return ok, nil
}
