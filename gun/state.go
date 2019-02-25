package gun

import (
	"bytes"
	"encoding/json"
	"time"
)

type State uint64

func StateNow() State { return State(timeNowUnixMs()) }

func StateFromTime(t time.Time) State { return State(timeToUnixMs(t)) }

type ConflictResolution int

const (
	ConflictResolutionNeverSeenUpdate ConflictResolution = iota
	ConflictResolutionTooFutureDeferred
	ConflictResolutionOlderHistorical
	ConflictResolutionNewerUpdate
	ConflictResolutionSameKeep
	ConflictResolutionSameUpdate
)

func (c ConflictResolution) IsImmediateUpdate() bool {
	return c == ConflictResolutionNeverSeenUpdate || c == ConflictResolutionNewerUpdate || c == ConflictResolutionSameUpdate
}

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
