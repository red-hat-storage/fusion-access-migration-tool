package main

// phase1Validations verifies the cluster is ready for migration before any
// destructive operations. Fails if required namespaces are missing or IBM FDF is
// already present — use --continue to skip preflight when resuming.
func phase1Validations(mc *MigrationContext) error {
	if err := validateClusterConnectivity(mc); err != nil {
		return err
	}
	if err := validateOCPVersion(mc); err != nil {
		return err
	}
	if err := validateExistingInstalls(mc); err != nil {
		return err
	}
	if err := validateCatalogAvailability(mc); err != nil {
		return err
	}
	if err := validateRequiredNamespaces(mc); err != nil {
		return err
	}
	return nil
}
