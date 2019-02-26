package gun

import (
	"bytes"
	"encoding/json"
	"time"
)

// State represents the conflict state of this value. It is usually the Unix
// time in milliseconds.
type State float64 // TODO: what if larger?

// StateNow is the current machine state (i.e. current Unix time in ms).
func StateNow() State {
	// TODO: Should I use timeNowUniqueUnix or otherwise set decimal spots to disambiguate?
	return State(timeNowUnixMs())
}

// StateFromTime converts a time to a State (i.e. converts to Unix ms).
func StateFromTime(t time.Time) State { return State(timeToUnixMs(t)) }

// ConflictResolution is how to handle two values for the same field.
type ConflictResolution int

const (
	// ConflictResolutionNeverSeenUpdate occurs when there is no existing value.
	// It means an update should always occur.
	ConflictResolutionNeverSeenUpdate ConflictResolution = iota
	// ConflictResolutionTooFutureDeferred occurs when the update is after our
	// current machine state. It means the update should be deferred.
	ConflictResolutionTooFutureDeferred
	// ConflictResolutionOlderHistorical occurs when the update happened before
	// the existing value's last update. It means it can be noted, but the
	// update should be discarded.
	ConflictResolutionOlderHistorical
	// ConflictResolutionNewerUpdate occurs when the update happened after last
	// update but is not beyond ur current machine state. It means the update
	// should overwrite.
	ConflictResolutionNewerUpdate
	// ConflictResolutionSameKeep occurs when the update happened at the same
	// time and it is lexically not the one chosen. It means the update should
	// be discarded.
	ConflictResolutionSameKeep
	// ConflictResolutionSameUpdate occurs when the update happened at the same
	// time and it is lexically the one chosen. It means the update should
	// overwrite.
	ConflictResolutionSameUpdate
)

// IsImmediateUpdate returns true for ConflictResolutionNeverSeenUpdate,
// ConflictResolutionNewerUpdate, and ConflictResolutionSameUpdate
func (c ConflictResolution) IsImmediateUpdate() bool {
	return c == ConflictResolutionNeverSeenUpdate || c == ConflictResolutionNewerUpdate || c == ConflictResolutionSameUpdate
}

// ConflictResolve checks the existing val/state, new val/state, and the current
// machine state to choose what to do with the update. Note, the existing val
// should always exist meaning it will never return
// ConflictResolutionNeverSeenUpdate.
func ConflictResolve(existingVal Value, existingState State, newVal Value, newState State, sysState State) ConflictResolution {
	// Existing gunjs impl serializes to JSON first to do lexical comparisons, so we will too
	if sysState < newState {
		return ConflictResolutionTooFutureDeferred
	} else if newState < existingState {
		return ConflictResolutionOlderHistorical
	} else if existingState < newState {
		return ConflictResolutionNewerUpdate
	} else if existingVal == newVal {
		return ConflictResolutionSameKeep
	} else if existingJSON, err := json.Marshal(existingVal); err != nil {
		panic(err)
	} else if newJSON, err := json.Marshal(newVal); err != nil {
		panic(err)
	} else if bytes.Compare(existingJSON, newJSON) < 0 {
		return ConflictResolutionSameUpdate
	} else {
		return ConflictResolutionSameKeep
	}
}
