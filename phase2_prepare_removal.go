package main

import "fmt"

// phase2PrepareFARemoval disables the RH FA operator and cleans up its managed
// resources so the operator and its CRDs can be safely removed.
func phase2PrepareFARemoval(mc *MigrationContext) error {
	if err := scaleDownFAOperatorCSV(mc); err != nil {
		return fmt.Errorf("scale down FA operator CSV: %w", err)
	}
	if err := removeValidatingWebhooks(mc); err != nil {
		return fmt.Errorf("remove validating webhooks: %w", err)
	}
	if err := removeOwnerRefsAndLabels(mc); err != nil {
		return fmt.Errorf("remove ownerReferences and labels: %w", err)
	}
	if err := removeFinalizersFromFilesystemClaims(mc); err != nil {
		return fmt.Errorf("remove finalizers from filesystemclaims: %w", err)
	}
	if err := deleteFilesystemClaims(mc); err != nil {
		return fmt.Errorf("delete filesystemclaims: %w", err)
	}
	return nil
}
