package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	envDryRun                  = "MIGRATION_DRY_RUN"
	envStateConfigMapName      = "MIGRATION_STATE_CONFIGMAP_NAME"
	envStateConfigMapNamespace = "MIGRATION_STATE_CONFIGMAP_NAMESPACE"
	envFDFCatalogImage         = "FDF_CATALOG_IMAGE"
)

type envConfig struct {
	DryRun                  bool
	StateConfigMapName      string
	StateConfigMapNamespace string
	FDFCatalogImage         string
}

func loadEnvConfig() (*envConfig, error) {
	cfg := &envConfig{
		DryRun:                  false,
		StateConfigMapName:      strings.TrimSpace(os.Getenv(envStateConfigMapName)),
		StateConfigMapNamespace: strings.TrimSpace(os.Getenv(envStateConfigMapNamespace)),
		FDFCatalogImage:         strings.TrimSpace(os.Getenv(envFDFCatalogImage)),
	}

	if dryRunRaw, ok := os.LookupEnv(envDryRun); ok && strings.TrimSpace(dryRunRaw) != "" {
		parsedDryRun, err := strconv.ParseBool(strings.TrimSpace(dryRunRaw))
		if err != nil {
			return nil, fmt.Errorf("%s must be a boolean value: %w", envDryRun, err)
		}
		cfg.DryRun = parsedDryRun
	}

	if cfg.StateConfigMapName == "" {
		return nil, fmt.Errorf("%s is required", envStateConfigMapName)
	}
	if cfg.StateConfigMapNamespace == "" {
		return nil, fmt.Errorf("%s is required", envStateConfigMapNamespace)
	}

	if !cfg.DryRun && cfg.FDFCatalogImage == "" {
		return nil, fmt.Errorf("%s is required when not in dry-run mode", envFDFCatalogImage)
	}

	return cfg, nil
}
