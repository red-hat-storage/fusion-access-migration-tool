package main

import (
	"testing"
)

func TestLoadEnvConfigSuccess(t *testing.T) {
	t.Setenv(envStateConfigMapName, "migration-state")
	t.Setenv(envStateConfigMapNamespace, "ibm-spectrum-scale")
	t.Setenv(envDryRun, "true")

	cfg, err := loadEnvConfig()
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !cfg.DryRun {
		t.Fatalf("expected dry run to be true")
	}
	if cfg.StateConfigMapName != "migration-state" {
		t.Fatalf("unexpected configmap name: %s", cfg.StateConfigMapName)
	}
	if cfg.StateConfigMapNamespace != "ibm-spectrum-scale" {
		t.Fatalf("unexpected configmap namespace: %s", cfg.StateConfigMapNamespace)
	}
}

func TestLoadEnvConfigRequiresConfigMapName(t *testing.T) {
	t.Setenv(envStateConfigMapName, "")
	t.Setenv(envStateConfigMapNamespace, "ibm-spectrum-scale")

	_, err := loadEnvConfig()
	if err == nil {
		t.Fatalf("expected an error when configmap name is missing")
	}
}

func TestLoadEnvConfigRequiresCatalogImageWhenNotDryRun(t *testing.T) {
	t.Setenv(envStateConfigMapName, "migration-state")
	t.Setenv(envStateConfigMapNamespace, "ibm-spectrum-scale")
	t.Setenv(envDryRun, "false")

	_, err := loadEnvConfig()
	if err == nil {
		t.Fatalf("expected an error when FDF_CATALOG_IMAGE is missing in non-dry-run mode")
	}
}

func TestLoadEnvConfigRequiresFusionOperatorCatalogImageWhenNotDryRun(t *testing.T) {
	t.Setenv(envStateConfigMapName, "migration-state")
	t.Setenv(envStateConfigMapNamespace, "ibm-spectrum-scale")
	t.Setenv(envDryRun, "false")
	t.Setenv(envFDFCatalogImage, "registry.example/fdf:latest")

	_, err := loadEnvConfig()
	if err == nil {
		t.Fatalf("expected an error when FUSION_OPERATOR_CATALOG_IMAGE is missing in non-dry-run mode")
	}
}

func TestLoadEnvConfigSuccessNonDryRunWithCatalogImages(t *testing.T) {
	t.Setenv(envStateConfigMapName, "migration-state")
	t.Setenv(envStateConfigMapNamespace, "ibm-spectrum-scale")
	t.Setenv(envDryRun, "false")
	t.Setenv(envFDFCatalogImage, "registry.example/fdf:latest")
	t.Setenv(envFusionOperatorCatalogImage, "icr.io/cpopen/ibm-operator-catalog:latest")

	cfg, err := loadEnvConfig()
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if cfg.DryRun {
		t.Fatalf("expected dry run false")
	}
	if cfg.FDFCatalogImage != "registry.example/fdf:latest" {
		t.Fatalf("unexpected FDF catalog image")
	}
	if cfg.FusionOperatorCatalogImage != "icr.io/cpopen/ibm-operator-catalog:latest" {
		t.Fatalf("unexpected Fusion Operator catalog image")
	}
}
