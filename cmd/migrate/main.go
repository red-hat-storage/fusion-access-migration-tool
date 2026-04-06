package main

import (
	"fmt"
	"os"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/phases"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/state"
)

const (
	exitCodeRetryableFailure = 1
	exitCodeFatalFailure     = 42
)

type migrationPhaseDef struct {
	id   int
	name string
	fn   func(*kube.Context) error
}

func printHeader() {
	output.PrintAppHeader()
}

func classifyFailure(phaseID int, err error) int {
	if phaseID == 1 {
		return exitCodeFatalFailure
	}
	return exitCodeRetryableFailure
}

func markFailedProgress(mc *kube.Context, progress state.MigrationProgress, phaseID int, err error) {
	if mc.DryRun {
		return
	}
	if markErr := state.MarkMigrationFailed(mc, progress, phaseID, err.Error()); markErr != nil {
		output.PrintWarning(fmt.Sprintf("Could not mark migration failed in state configmap: %v", markErr))
	}
}

func determineStartPhase(dryRun bool, progress state.MigrationProgress) (startPhase int, resumingFromCheckpoint bool, alreadyComplete bool) {
	if dryRun {
		return 1, false, false
	}
	if progress.LastCompletedPhase >= 6 || progress.Status == state.StatusCompleted {
		return 7, false, true
	}
	if progress.LastCompletedPhase > 0 {
		return progress.LastCompletedPhase + 1, true, false
	}
	return 1, false, false
}

func main() {
	cfg, err := loadEnvConfig()
	if err != nil {
		output.PrintError(fmt.Sprintf("Invalid environment configuration: %v", err))
		os.Exit(exitCodeFatalFailure)
	}

	printHeader()
	if cfg.DryRun {
		fmt.Println()
		output.PrintWarning("Dry-run mode enabled — no changes will be made and migration checkpoint state will not be updated")
	}

	mc, err := kube.NewInClusterContext()
	if err != nil {
		output.PrintError(fmt.Sprintf("Failed to initialize in-cluster Kubernetes clients: %v", err))
		os.Exit(exitCodeFatalFailure)
	}
	mc.DryRun = cfg.DryRun
	mc.StateConfigMapName = cfg.StateConfigMapName
	mc.StateConfigMapNamespace = cfg.StateConfigMapNamespace
	mc.FDFCatalogImage = cfg.FDFCatalogImage

	progress, err := state.ReadMigrationProgress(mc)
	if err != nil {
		output.PrintError(fmt.Sprintf("Failed to read migration progress from ConfigMap: %v", err))
		os.Exit(exitCodeFatalFailure)
	}
	if !cfg.DryRun {
		mc.SecureBootClusterForKMM = progress.SecureBootClusterForKMM
	}

	phaseDefs := []migrationPhaseDef{
		{1, "Preflight validations", phases.PreflightValidations},
		{2, "Prepare Fusion Access for removal", phases.PrepareFusionAccessRemoval},
		{3, "Uninstall Fusion Access and Scale", phases.UninstallFusionAccessAndScale},
		{4, "Install Data Foundation", phases.InstallDataFoundation},
		{5, "Migrate KMM", phases.MigrateKMM},
		{6, "Finalize storage configuration", phases.FinalizeStorageConfiguration},
	}

	startPhase, resumingFromCheckpoint, alreadyComplete := determineStartPhase(cfg.DryRun, progress)
	if alreadyComplete {
		fmt.Println()
		output.PrintSuccess("Migration already marked complete in state ConfigMap; nothing to do")
		fmt.Println()
		return
	}
	if resumingFromCheckpoint {
		mc.ResumingFromCheckpoint = true
		output.PrintInfo(fmt.Sprintf("Resume mode enabled from checkpoint: lastCompletedPhase=%d", progress.LastCompletedPhase))
		output.PrintInfo("Skipping preflight based on checkpoint")
	} else if cfg.DryRun && progress.LastCompletedPhase > 0 {
		output.PrintInfo("Dry-run ignores persisted checkpoint and runs all phases from the beginning")
	}

	for _, phase := range phaseDefs {
		if phase.id < startPhase {
			continue
		}
		output.PrintPhase(phase.name)

		if err := phase.fn(mc); err != nil {
			output.PrintError(fmt.Sprintf("%s failed: %v", phase.name, err))
			markFailedProgress(mc, progress, phase.id, err)
			os.Exit(classifyFailure(phase.id, err))
		}

		if !cfg.DryRun {
			progress.LastCompletedPhase = phase.id
			progress.SecureBootClusterForKMM = mc.SecureBootClusterForKMM
			progress.Status = state.StatusInProgress
			progress.FailedPhase = 0
			progress.FailureReason = ""

			if err := state.WriteMigrationProgress(mc, progress); err != nil {
				output.PrintError(fmt.Sprintf("write migration progress failed after %s: %v", phase.name, err))
				os.Exit(exitCodeRetryableFailure)
			}
		}

		output.PrintSuccess(phase.name + " completed")
	}

	if !cfg.DryRun {
		if err := state.MarkMigrationCompleted(mc, progress); err != nil {
			output.PrintWarning(fmt.Sprintf("Could not mark migration completed in state configmap: %v", err))
		}
	}

	fmt.Println()
	output.PrintSuccess("Migration completed successfully!")
	fmt.Println()
}
