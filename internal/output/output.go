package output

import (
	"fmt"
	"time"
)

const (
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorReset  = "\033[0m"
)

func linePrefix() string {
	return time.Now().Format(time.RFC3339Nano) + " "
}

func printf(format string, a ...interface{}) {
	fmt.Printf(linePrefix()+format, a...)
}

// PrintAppHeader prints the migration tool banner (stdout, timestamp-prefixed lines).
func PrintAppHeader() {
	fmt.Print("\n")
	printf("%s========================================%s\n", ColorBlue, ColorReset)
	printf("%s  Fusion Access Migration Tool%s\n", ColorBlue, ColorReset)
	printf("%s========================================%s\n", ColorBlue, ColorReset)
	printf("  Migrating IBM Spectrum Scale 6.0.0.2 (RH FA) → 6.0.1.0 (FDF)\n")
}

func PrintPhase(name string) {
	fmt.Print("\n")
	printf("%s══════════════════════════════════════════════════════════════%s\n", ColorBlue, ColorReset)
	printf("%s  %s%s\n", ColorBlue, name, ColorReset)
	printf("%s══════════════════════════════════════════════════════════════%s\n", ColorBlue, ColorReset)
}

func PrintSuccess(msg string) {
	printf("  %s✓ %s%s\n", ColorGreen, msg, ColorReset)
}

func PrintSkip(msg string) {
	printf("  %s- %s (skipped)%s\n", ColorYellow, msg, ColorReset)
}

func PrintDryRun(msg string) {
	printf("  %s[dry-run] %s%s\n", ColorYellow, msg, ColorReset)
}

func PrintError(msg string) {
	printf("  %s✗ %s%s\n", ColorRed, msg, ColorReset)
}

func PrintInfo(msg string) {
	printf("  %s\n", msg)
}

func PrintWarning(msg string) {
	printf("  %sWARNING: %s%s\n", ColorYellow, msg, ColorReset)
}
