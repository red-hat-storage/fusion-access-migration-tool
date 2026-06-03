package main

import (
	"testing"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/state"
)

func TestDetermineStartPhase(t *testing.T) {
	tests := []struct {
		name                       string
		dryRun                     bool
		progress                   state.MigrationProgress
		wantStartPhase             int
		wantResumingFromCheckpoint bool
		wantComplete               bool
	}{
		{
			name:                       "fresh run starts from phase 1",
			dryRun:                     false,
			progress:                   state.MigrationProgress{},
			wantStartPhase:             1,
			wantResumingFromCheckpoint: false,
			wantComplete:               false,
		},
		{
			name:   "resume starts from next phase",
			dryRun: false,
			progress: state.MigrationProgress{
				LastCompletedPhase: 3,
				Status:             state.StatusInProgress,
			},
			wantStartPhase:             4,
			wantResumingFromCheckpoint: true,
			wantComplete:               false,
		},
		{
			name:   "completed migration exits early",
			dryRun: false,
			progress: state.MigrationProgress{
				LastCompletedPhase: 7,
				Status:             state.StatusCompleted,
			},
			wantStartPhase:             8,
			wantResumingFromCheckpoint: false,
			wantComplete:               true,
		},
		{
			name:   "legacy completed migration with phase 6 exits early",
			dryRun: false,
			progress: state.MigrationProgress{
				LastCompletedPhase: 6,
				Status:             state.StatusCompleted,
			},
			wantStartPhase:             8,
			wantResumingFromCheckpoint: false,
			wantComplete:               true,
		},
		{
			name:   "resume after finalize storage runs Fusion Operator phase",
			dryRun: false,
			progress: state.MigrationProgress{
				LastCompletedPhase: 6,
				Status:             state.StatusInProgress,
			},
			wantStartPhase:             7,
			wantResumingFromCheckpoint: true,
			wantComplete:               false,
		},
		{
			name:   "dry run always starts from phase 1",
			dryRun: true,
			progress: state.MigrationProgress{
				LastCompletedPhase: 4,
				Status:             state.StatusInProgress,
			},
			wantStartPhase:             1,
			wantResumingFromCheckpoint: false,
			wantComplete:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotResuming, gotComplete := determineStartPhase(tt.dryRun, tt.progress)
			if gotStart != tt.wantStartPhase {
				t.Fatalf("start phase mismatch: got %d, want %d", gotStart, tt.wantStartPhase)
			}
			if gotResuming != tt.wantResumingFromCheckpoint {
				t.Fatalf("resuming from checkpoint mismatch: got %t, want %t", gotResuming, tt.wantResumingFromCheckpoint)
			}
			if gotComplete != tt.wantComplete {
				t.Fatalf("complete mismatch: got %t, want %t", gotComplete, tt.wantComplete)
			}
		})
	}
}
