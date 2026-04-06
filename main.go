package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

var (
	dryRun                 bool
	storageClassDir        string
	continueMigration      bool
	migrationStateFileFlag string
)

type migrationPhaseDef struct {
	id   int
	name string
	fn   func(*MigrationContext) error
}

func printUsage() {
	fmt.Println(`Usage: fusion-access-migration-tool [OPTIONS]

Fusion Access to Data Foundation migration tool — runs phases in order:
  1. Validations (OCP version, existing installs, catalog, namespaces)
  2. Prepare Fusion Access for Removal (scale down, webhooks, ownerRefs, labels, finalizers)
  3. Uninstall Fusion Access and Scale Components (subscriptions, CSVs, operator scaledown)
  4. Install Data Foundation (FDF subscription, Grafana Bridge, namespace labeling)
  5. KMM Migration (list/delete modules.kmm.sigs.x-k8s.io in ibm-fusion-access, delete ibm-fusion-access, KMM subscription, Scale cluster update)
  6. Final Configuration (GPFS health, Spectrum Scale CSI StorageClasses, filesystem recovery verification)

OPTIONS:
    -d, --dry-run              Preview changes without applying them (does not update progress file)
        --continue             Skip Phase 1 preflight (for resume after failure or partial run)
        --state-file PATH      Migration progress JSON (default: .fusion-access-migration-progress in cwd)
        --storage-class-dir    Reserved for future StorageClass creation from YAML (not used yet)
    -h, --help                 Display this help message

EXAMPLES:
    fusion-access-migration-tool --dry-run              Preview only (runs rigid preflight)
    fusion-access-migration-tool                        Apply changes
    fusion-access-migration-tool --continue             Resume without preflight
    fusion-access-migration-tool --dry-run --continue     Show phase status and pending steps from last run`)
}

func printDryRunContinueSummary(statePath string, lastCompleted int, phases []migrationPhaseDef) {
	fmt.Println()
	printInfo("Phase status (--dry-run --continue; checkpoint from last non-dry-run run)")
	printInfo(fmt.Sprintf("State file: %s", statePath))
	if lastCompleted == 0 {
		printWarning("No checkpoint — completed phases unknown; phases 2–6 shown as pending")
	}
	var pending []string
	for _, p := range phases {
		var status string
		switch {
		case p.id == 1:
			status = "skipped (preflight not run — --continue)"
		case p.id <= lastCompleted:
			status = "completed (checkpoint)"
		default:
			status = "pending"
			if p.id > 1 {
				pending = append(pending, p.name)
			}
		}
		printInfo(fmt.Sprintf("  %s — %s", p.name, status))
	}
	fmt.Println()
	if len(pending) > 0 {
		printInfo("Yet to run: " + strings.Join(pending, "; "))
	} else if lastCompleted >= 6 {
		printInfo("Yet to run: (none — checkpoint indicates migration finished; state file should have been removed)")
	} else {
		printInfo("Yet to run: (none in phases 2–6 per checkpoint; re-run without --dry-run to execute)")
	}
	fmt.Println()
}

func printHeader() {
	fmt.Printf("\n%s========================================%s\n", colorBlue, colorReset)
	fmt.Printf("%s  Fusion Access Migration Tool%s\n", colorBlue, colorReset)
	fmt.Printf("%s========================================%s\n", colorBlue, colorReset)
	fmt.Println("  Migrating IBM Spectrum Scale 6.0.0.2 (RH FA) → 6.0.1.0 (FDF)")
}

func main() {
	flag.BoolVar(&dryRun, "dry-run", false, "Preview changes without applying them")
	flag.BoolVar(&dryRun, "d", false, "Preview changes without applying them (shorthand)")
	flag.BoolVar(&continueMigration, "continue", false, "Skip Phase 1 preflight (resume migration)")
	flag.StringVar(&migrationStateFileFlag, "state-file", "", fmt.Sprintf("Migration progress file (default %q)", defaultMigrationStateFile))
	flag.StringVar(&storageClassDir, "storage-class-dir", "", "Reserved for future StorageClass YAML apply (not used yet)")
	help := flag.Bool("help", false, "Display help message")
	flag.BoolVar(help, "h", false, "Display help message (shorthand)")
	flag.Parse()

	if *help {
		printUsage()
		os.Exit(0)
	}

	printHeader()

	if dryRun {
		fmt.Println()
		printWarning("Dry-run mode enabled — no changes will be made")
	}
	if continueMigration {
		fmt.Println()
		printInfo("Continue mode — Phase 1 preflight will be skipped")
	}

	mc, err := initializeClients()
	if err != nil {
		printError(fmt.Sprintf("Failed to initialize Kubernetes clients: %v", err))
		os.Exit(1)
	}

	statePath := effectiveMigrationStatePath(migrationStateFileFlag)

	phases := []migrationPhaseDef{
		{1, "Phase 1: Validations", phase1Validations},
		{2, "Phase 2: Prepare Fusion Access for Removal", phase2PrepareFARemoval},
		{3, "Phase 3: Uninstall Fusion Access and Scale Components", phase3UninstallFAAndScale},
		{4, "Phase 4: Install Data Foundation", phase4InstallDataFoundation},
		{5, "Phase 5: KMM Migration", phase5KMMMigration},
		{6, "Phase 6: Final Configuration", phase6FinalConfiguration},
	}

	if dryRun && continueMigration {
		lastCompleted, rerr := readLastCompletedPhase(statePath)
		if rerr != nil {
			printWarning(fmt.Sprintf("Could not read migration state: %v — assuming no phases completed", rerr))
			lastCompleted = 0
		}
		printDryRunContinueSummary(statePath, lastCompleted, phases)
	}

	if !continueMigration {
		p := phases[0]
		printPhase(p.name)
		if err := p.fn(mc); err != nil {
			printError(fmt.Sprintf("%s failed: %v", p.name, err))
			os.Exit(1)
		}
		if !dryRun {
			if err := writeLastCompletedPhase(statePath, 1); err != nil {
				printError(fmt.Sprintf("write migration state: %v", err))
				os.Exit(1)
			}
		}
		printSuccess(p.name + " completed")
	} else {
		printInfo("Skipping Phase 1 (preflight) — --continue")
	}

	for _, p := range phases[1:] {
		printPhase(p.name)
		if err := p.fn(mc); err != nil {
			printError(fmt.Sprintf("%s failed: %v", p.name, err))
			os.Exit(1)
		}
		if !dryRun {
			if p.id < 6 {
				if err := writeLastCompletedPhase(statePath, p.id); err != nil {
					printError(fmt.Sprintf("write migration state: %v", err))
					os.Exit(1)
				}
			}
		}
		printSuccess(p.name + " completed")
	}

	if !dryRun {
		if err := removeMigrationStateFile(statePath); err != nil {
			printWarning(fmt.Sprintf("Could not remove migration state file %s: %v", statePath, err))
		}
	}

	fmt.Println()
	printSuccess("Migration completed successfully!")
	fmt.Println()
}
