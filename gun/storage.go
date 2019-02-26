package gun

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrStorageNotFound is returned by Storage.Get and sometimes Storage.Put when
// the field doesn't exist.
var ErrStorageNotFound = errors.New("Not found")

// Storage is the interface that storage adapters must implement.
type Storage interface {
	// Get obtains the value (which can be nil) and state from storage for the
	// given field. If the field does not exist, this errors with
	// ErrStorageNotFound.
	Get(ctx context.Context, parentSoul, field string) (Value, State, error)
	// Put sets the value (which can be nil) and state in storage for the given
	// field if the conflict resolution says it should (see ConflictResolve). It
	// also returns the conflict resolution. If onlyIfExists is true and the
	// field does not exist, this errors with ErrStorageNotFound. Otherwise, if
	// the resulting resolution is an immediate update, it is done. If the
	// resulting resolution is deferred for the future, it is scheduled for then
	// but is not even attempted if context is completed or storage is closed.
	Put(ctx context.Context, parentSoul, field string, val Value, state State, onlyIfExists bool) (ConflictResolution, error)
	// Close closes this storage and disallows future gets or puts.
	Close() error
}

type storageInMem struct {
	values        map[parentSoulAndField]*valueWithState
	valueLock     sync.RWMutex
	closed        bool // Do not mutate outside of valueLock
	purgeCancelFn context.CancelFunc
}

type parentSoulAndField struct{ parentSoul, field string }

type valueWithState struct {
	val   Value
	state State
}

// NewStorageInMem creates an in-memory storage that automatically purges
// values that are older than the given oldestAllowed. If oldestAllowed is 0,
// it keeps all values forever.
func NewStorageInMem(oldestAllowed time.Duration) Storage {
	s := &storageInMem{values: map[parentSoulAndField]*valueWithState{}}
	// Start the purger
	if oldestAllowed > 0 {
		var ctx context.Context
		ctx, s.purgeCancelFn = context.WithCancel(context.Background())
		go func() {
			tick := time.NewTicker(5 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case t := <-tick.C:
					oldestStateAllowed := StateFromTime(t.Add(-oldestAllowed))
					s.valueLock.Lock()
					s.valueLock.Unlock()
					for k, v := range s.values {
						if v.state < oldestStateAllowed {
							delete(s.values, k)
						}
					}
				}
			}
		}()
	}
	return s
}

func (s *storageInMem) Get(ctx context.Context, parentSoul, field string) (Value, State, error) {
	s.valueLock.RLock()
	defer s.valueLock.RUnlock()
	if s.closed {
		return nil, 0, fmt.Errorf("Storage closed")
	} else if vs := s.values[parentSoulAndField{parentSoul, field}]; vs == nil {
		return nil, 0, ErrStorageNotFound
	} else {
		return vs.val, vs.state, nil
	}
}

func (s *storageInMem) Put(
	ctx context.Context, parentSoul, field string, val Value, state State, onlyIfExists bool,
) (confRes ConflictResolution, err error) {
	s.valueLock.Lock()
	defer s.valueLock.Unlock()
	key, newVs := parentSoulAndField{parentSoul, field}, &valueWithState{val, state}
	sysState := StateNow()
	if s.closed {
		return 0, fmt.Errorf("Storage closed")
	} else if existingVs := s.values[key]; existingVs == nil && onlyIfExists {
		return 0, ErrStorageNotFound
	} else if existingVs == nil {
		confRes = ConflictResolutionNeverSeenUpdate
	} else {
		confRes = ConflictResolve(existingVs.val, existingVs.state, val, state, sysState)
	}
	if confRes == ConflictResolutionTooFutureDeferred {
		// Schedule for 100ms past when it's deferred to
		time.AfterFunc(time.Duration(state-sysState)*time.Millisecond+100, func() {
			s.valueLock.RLock()
			closed := s.closed
			s.valueLock.RUnlock()
			// TODO: what to do w/ error?
			if !closed && ctx.Err() == nil {
				s.Put(ctx, parentSoul, field, val, state, onlyIfExists)
			}
		})
	} else if confRes.IsImmediateUpdate() {
		s.values[key] = newVs
	}
	return
}

func (s *storageInMem) Close() error {
	if s.purgeCancelFn != nil {
		s.purgeCancelFn()
	}
	s.valueLock.Lock()
	defer s.valueLock.Unlock()
	s.closed = true
	return nil
}
