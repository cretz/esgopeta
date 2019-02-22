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

func (s *StorageInMem) Get(ctx context.Context, parentSoul, field string) (*ValueWithState, error) {
	panic("TODO")
}

func (s *StorageInMem) Put(ctx context.Context, parentSoul, field string, val *ValueWithState) (bool, error) {
	panic("TODO")
}
