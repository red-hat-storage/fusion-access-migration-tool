package main

import "fmt"

const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorReset  = "\033[0m"
)

func printPhase(name string) {
	fmt.Printf("\n%s══════════════════════════════════════════════════════════════%s\n", colorBlue, colorReset)
	fmt.Printf("%s  %s%s\n", colorBlue, name, colorReset)
	fmt.Printf("%s══════════════════════════════════════════════════════════════%s\n", colorBlue, colorReset)
}

func printSuccess(msg string) {
	fmt.Printf("  %s✓ %s%s\n", colorGreen, msg, colorReset)
}

func printSkip(msg string) {
	fmt.Printf("  %s- %s (skipped)%s\n", colorYellow, msg, colorReset)
}

func printDryRun(msg string) {
	fmt.Printf("  %s[dry-run] %s%s\n", colorYellow, msg, colorReset)
}

func printError(msg string) {
	fmt.Printf("  %s✗ %s%s\n", colorRed, msg, colorReset)
}

func printInfo(msg string) {
	fmt.Printf("  %s\n", msg)
}

func printWarning(msg string) {
	fmt.Printf("  %sWARNING: %s%s\n", colorYellow, msg, colorReset)
}
