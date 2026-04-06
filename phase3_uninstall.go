package main

import "fmt"

// phase3UninstallFAAndScale removes the RH FA OLM subscription and CSV, then
// scales down the IBM Spectrum Scale operator. Subscription/CSV removal comes
// first to prevent OLM from reconciling a partially-removed operator.
func phase3UninstallFAAndScale(mc *MigrationContext) error {
	if err := removeSubscriptionAndCSV(mc); err != nil {
		return fmt.Errorf("remove FA subscription and CSV: %w", err)
	}
	if err := scaleDownSpectrumScaleOperator(mc); err != nil {
		return fmt.Errorf("scale down Spectrum Scale operator: %w", err)
	}
	return nil
}
