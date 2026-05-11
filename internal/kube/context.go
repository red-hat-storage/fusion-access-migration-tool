package kube

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Context carries Kubernetes clients and run options for migration steps.
type Context struct {
	Clientset  kubernetes.Interface
	Dynamic    dynamic.Interface
	RESTConfig *rest.Config
	Ctx        context.Context
	DryRun     bool
	// ResumingFromCheckpoint is set when migration progress in the state ConfigMap shows a prior phase completed (Job restart).
	// It is not a CLI flag: it enables relaxed checks (e.g. existing SAN StorageClasses, FDF 4.21.x after install) when preflight is skipped.
	ResumingFromCheckpoint bool
	// FDFCatalogImage is the container image for the isf-data-foundation-catalog CatalogSource (FDF_CATALOG_IMAGE).
	FDFCatalogImage string
	// FusionOperatorCatalogImage is the container image for the ibm-operator-catalog CatalogSource (FUSION_OPERATOR_CATALOG_IMAGE).
	FusionOperatorCatalogImage string
	StateConfigMapNamespace    string
	StateConfigMapName         string
	// SecureBootClusterForKMM is set during phase 3 (UninstallFusionAccessAndScale) from CopySecureBootSigningSecretsIfPresent; used by MigrateKMM for EnableKMMInScaleCluster.
	SecureBootClusterForKMM bool
}

// NewInClusterContext builds clients from in-cluster service account configuration.
func NewInClusterContext() (*Context, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &Context{
		Clientset:  clientset,
		Dynamic:    dynamicClient,
		RESTConfig: config,
		Ctx:        context.Background(),
	}, nil
}
