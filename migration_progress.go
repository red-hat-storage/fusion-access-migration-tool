package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const defaultMigrationStateFile = ".fusion-access-migration-progress"

type migrationProgressJSON struct {
	LastCompletedPhase int `json:"lastCompletedPhase"`
}

func effectiveMigrationStatePath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return defaultMigrationStateFile
}

func readLastCompletedPhase(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var p migrationProgressJSON
	if err := json.Unmarshal(data, &p); err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if p.LastCompletedPhase < 0 || p.LastCompletedPhase > 6 {
		return 0, fmt.Errorf("invalid lastCompletedPhase %d in %s", p.LastCompletedPhase, path)
	}
	return p.LastCompletedPhase, nil
}

func writeLastCompletedPhase(path string, n int) error {
	if n < 1 || n > 6 {
		return fmt.Errorf("lastCompletedPhase out of range: %d", n)
	}
	p := migrationProgressJSON{LastCompletedPhase: n}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func removeMigrationStateFile(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
