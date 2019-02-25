package gun

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrStorageNotFound = errors.New("Not found")

type Storage interface {
	Get(ctx context.Context, parentSoul, field string) (Value, State, error)
	Put(ctx context.Context, parentSoul, field string, val Value, state State, onlyIfExists bool) (ConflictResolution, error)
	Close() error
}

type storageInMem struct {
	values        map[parentSoulAndField]*valueWithState
	valueLock     sync.RWMutex
	purgeCancelFn context.CancelFunc
}

type parentSoulAndField struct{ parentSoul, field string }

type valueWithState struct {
	val   Value
	state State
}

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
	if vs := s.values[parentSoulAndField{parentSoul, field}]; vs == nil {
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
	if existingVs := s.values[key]; existingVs == nil && onlyIfExists {
		return 0, ErrStorageNotFound
	} else if existingVs == nil {
		confRes = ConflictResolutionNeverSeenUpdate
	} else {
		confRes = ConflictResolve(existingVs.val, existingVs.state, val, state, sysState)
	}
	if confRes == ConflictResolutionTooFutureDeferred {
		// Schedule for 100ms past when it's deferred to
		time.AfterFunc(time.Duration(state-sysState)*time.Millisecond+100, func() {
			// TODO: should I check whether closed?
			// TODO: what to do w/ error?
			s.Put(ctx, parentSoul, field, val, state, onlyIfExists)
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
	return nil
}
