package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// migrateLegacySingleStationState moves a pre-fleet single station's
// persisted state — its engine/timeline snapshot (baseDir/engine) and
// offline message queue (baseDir/message_queue.json,
// baseDir/message_dead_letter.jsonl) — into its new station-scoped
// directory, the first time the process runs with more than one station
// configured. Without this, adding a second station silently abandoned the
// original station's meter readings, session history, and offline queue —
// its runtime would start fresh at zero instead of continuing from where
// the single-station deployment left off.
//
// The existing-stationDir check doubles as the "already migrated" marker,
// so this is safe to call on every startup: a second call is a no-op.
// Returns whether a migration actually happened. Failures are non-fatal —
// logged by the caller and the station starts fresh — since the common case
// (a config that was never single-station, or was migrated long ago) has no
// legacy directory to find and must not be treated as an error.
func migrateLegacySingleStationState(baseDir, stationDir string) (bool, error) {
	if _, err := os.Stat(stationDir); err == nil {
		return false, nil
	}
	legacyEngineDir := filepath.Join(baseDir, "engine")
	if _, err := os.Stat(legacyEngineDir); err != nil {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(stationDir), 0750); err != nil {
		return false, fmt.Errorf("create station dir parent: %w", err)
	}
	if err := os.Rename(legacyEngineDir, stationDir); err != nil {
		return false, fmt.Errorf("move engine state: %w", err)
	}

	var queueErr error
	for _, name := range []string{"message_queue.json", "message_dead_letter.jsonl"} {
		src := filepath.Join(baseDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(stationDir, name)); err != nil && queueErr == nil {
			queueErr = fmt.Errorf("move %s: %w", name, err)
		}
	}
	return true, queueErr
}
