package gun

import (
	"context"
	"errors"
	"sync"
)

var ErrStorageNotFound = errors.New("Not found")

type Storage interface {
	Get(ctx context.Context, parentSoul, field string) (*ValueWithState, error)
	Put(ctx context.Context, parentSoul, field string, val *ValueWithState) (bool, error)
	// Tracking(ctx context.Context, id string) (bool, error)
}

type StorageInMem struct {
	values sync.Map
}

type parentSoulAndField struct{ parentSoul, field string }

func (s *StorageInMem) Get(ctx context.Context, parentSoul, field string) (*ValueWithState, error) {
	v, ok := s.values.Load(parentSoulAndField{parentSoul, field})
	if !ok {
		return nil, ErrStorageNotFound
	}
	return v.(*ValueWithState), nil
}

func (s *StorageInMem) Put(ctx context.Context, parentSoul, field string, val *ValueWithState) (bool, error) {
	s.values.Store(parentSoulAndField{parentSoul, field}, val)
	// TODO: conflict resolution state check?
	return true, nil
}
