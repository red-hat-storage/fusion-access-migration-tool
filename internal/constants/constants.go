package constants

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	FusionAccessNS                     = "ibm-fusion-access"
	SpectrumScaleNS                    = "ibm-spectrum-scale"
	SpectrumScaleOperatorNS            = "ibm-spectrum-scale-operator"
	SpectrumScaleOperatorCSVNamePrefix = "ibm-spectrum-scale-operator."
	KmmNS                              = "openshift-kmm"
	KmmOperatorGroupName               = "openshift-kmm-operatorgroup"
	UserWorkloadMonitoringNS           = "openshift-user-workload-monitoring"
	OpenShiftStorageNS                 = "openshift-storage"
	OpenShiftStorageOperatorGroupName  = "fdf-operator-group"
	OpenShiftMarketplaceNS             = "openshift-marketplace"
	// FusionOperatorNS is the namespace for IBM Spectrum Fusion Operator (isf-operator Subscription).
	FusionOperatorNS = "ibm-spectrum-fusion-ns"
	// FusionOperatorGroupName is the OperatorGroup for Fusion Operator in FusionOperatorNS.
	FusionOperatorGroupName = "isf-og"
	// FusionOperatorCatalogSourceName is the CatalogSource for IBM Operator Catalog in openshift-marketplace.
	FusionOperatorCatalogSourceName = "ibm-operator-catalog"
	// FusionOperatorSubscriptionName is the Subscription for isf-operator in FusionOperatorNS.
	FusionOperatorSubscriptionName = "isf-operator"
	// FusionOperatorSubscriptionChannel is the OLM channel for isf-operator.
	FusionOperatorSubscriptionChannel = "v2.0"
	// FDFCatalogSourceName is the CatalogSource name for IBM Spectrum Fusion Data Foundation in openshift-marketplace.
	FDFCatalogSourceName = "isf-data-foundation-catalog"

	OdfSubscriptionChannel = "stable-4.21"
	// OdfOperatorCreatorFusionLabelKey is applied to the odf-operator Subscription for FDF install parity with Fusion tooling.
	OdfOperatorCreatorFusionLabelKey = "cns.isf.ibm.com/creator-fusion"

	SpectrumScaleController       = "ibm-spectrum-scale-controller-manager"
	LocalDiskResource             = "localdisks.scale.spectrum.ibm.com"
	FilesystemResource            = "filesystems.scale.spectrum.ibm.com"
	FilesystemClaimResource       = "filesystemclaims.fusion.storage.openshift.io"
	FusionAccessOperatorName      = "openshift-fusion-access-operator"
	IBMEntitlementKeySecret       = "ibm-entitlement-key"
	SecureBootSigningKeySecret    = "secureboot-signing-key"
	SecureBootSigningKeyPubSecret = "secureboot-signing-key-pub"
	OdfOperatorSubPrefix          = "odf-operator"
	OdfProviderRedHat             = "Red Hat"
	OdfProviderIBM                = "IBM"

	KmmModulesResource = "modules.kmm.sigs.x-k8s.io"
	RequiredOCPVersion = "4.21"

	// RequiredFDFMajor — IBM odf-operator CSV spec.version uses major 4; preflight expects minor 20 unless resuming (see cluster.FdfOdfPreflightAllowed).
	RequiredFDFMajor uint64 = 4

	// Scale / node labels used when patching the Fusion Access KMM Module selector for migration.
	ScaleNodeLabelImageDigest = "scale.spectrum.ibm.com/image-digest"
	ScaleNodeLabelRole        = "scale.spectrum.ibm.com/role"
	ScaleNodeRoleStorage      = "storage"
	NodeLabelArch             = "kubernetes.io/arch"

	SpectrumScaleCSIProvisioner = "spectrumscale.csi.ibm.com"

	// GrafanaBridgeWebhook* — Scale cluster mutating webhook may temporarily have no Service endpoints; wait before retrying Update.
	GrafanaBridgeWebhookNoEndpointsRetryWait   = 5 * time.Minute
	GrafanaBridgeWebhookNoEndpointsMaxAttempts = 12

	FAOperatorScaleDownWaitTimeout          = 10 * time.Minute
	FAOperatorScaleDownPollInterval         = 5 * time.Second
	FusionAccessNamespaceDeleteWaitTimeout  = 15 * time.Minute
	FusionAccessNamespaceDeletePollInterval = 3 * time.Second
	// KmmModuleDelete* — wait for each modules.kmm object to disappear after Delete (namespace delete is blocked until gone).
	KmmModuleDeleteWaitTimeout  = 15 * time.Minute
	KmmModuleDeletePollInterval = 5 * time.Second
	// KmmModuleNodesMatching* — wait for status.moduleLoader.nodesMatchingSelectorNumber to become 0 after Scale cluster enables KMM.
	KmmModuleNodesMatchingWaitTimeout  = 30 * time.Minute
	KmmModuleNodesMatchingPollInterval = 5 * time.Second
	// FilesystemRecoveryWait* — wait for Spectrum Scale filesystems to report mounted=true during finalization.
	FilesystemRecoveryWaitTimeout  = 10 * time.Minute
	FilesystemRecoveryPollInterval = 30 * time.Second
	// FDFCatalogSourceReady* — wait for isf-data-foundation-catalog CatalogSource gRPC ready after create/update.
	FDFCatalogSourceReadyTimeout      = 10 * time.Minute
	FDFCatalogSourceReadyPollInterval = 1 * time.Minute
)

var (
	SubscriptionGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "subscriptions",
	}
	CsvGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions",
	}
	OperatorGroupGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1", Resource: "operatorgroups",
	}
	ClusterVersionGVR = schema.GroupVersionResource{
		Group: "config.openshift.io", Version: "v1", Resource: "clusterversions",
	}
	CatalogSourceGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "catalogsources",
	}
	KmmModuleGVR = schema.GroupVersionResource{
		Group: "kmm.sigs.x-k8s.io", Version: "v1beta1", Resource: "modules",
	}
)
