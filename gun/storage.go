package gun

import (
	"context"
	"errors"
	"sync"
)

var ErrStorageNotFound = errors.New("Not found")

type Storage interface {
	Get(ctx context.Context, parentID, field string) (*StatefulValue, error)
	Put(ctx context.Context, parentID, field string, val *StatefulValue) (bool, error)
	// Tracking(ctx context.Context, id string) (bool, error)
}

type StorageInMem struct {
	values sync.Map
}

func (s *StorageInMem) Get(ctx context.Context, parentID, field string) (*StatefulValue, error) {
	panic("TODO")
}

func (s *StorageInMem) Put(ctx context.Context, parentID, field string, val *StatefulValue) (bool, error) {
	panic("TODO")
}
