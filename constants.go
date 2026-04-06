package main

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	fusionAccessNS                    = "ibm-fusion-access"
	spectrumScaleNS                   = "ibm-spectrum-scale"
	spectrumScaleOperatorNS           = "ibm-spectrum-scale-operator"
	// IBM Spectrum Scale operator CSV from FDF uses names like ibm-spectrum-scale-operator.v60.1.0
	// in namespace ibm-spectrum-scale (workload namespace for Scale).
	spectrumScaleOperatorCSVNamePrefix = "ibm-spectrum-scale-operator."
	kmmNS                             = "openshift-kmm"
	kmmOperatorGroupName              = "openshift-kmm-operatorgroup"
	userWorkloadMonitoringNS          = "openshift-user-workload-monitoring"
	openShiftStorageNS                = "openshift-storage"
	openShiftStorageOperatorGroupName = "openshift-storage-operatorgroup"
	openShiftMarketplaceNS            = "openshift-marketplace"

	// odfSubscriptionChannel must match the OCP minor in requiredOCPVersion (e.g. 4.21 → stable-4.21).
	odfSubscriptionChannel = "stable-4.21"

	spectrumScaleController  = "ibm-spectrum-scale-controller-manager"
	localDiskResource        = "localdisks.scale.spectrum.ibm.com"
	filesystemResource       = "filesystems.scale.spectrum.ibm.com"
	filesystemClaimResource  = "filesystemclaims.fusion.storage.openshift.io"
	fusionAccessOperatorName = "openshift-fusion-access-operator"
	odfOperatorSubPrefix     = "odf-operator"
	odfProviderRedHat        = "Red Hat"
	odfProviderIBM           = "IBM"

	gpfsModuleName = "gpfs-module"
	// kmmModulesResource matches: oc get modules.kmm.sigs.x-k8s.io
	kmmModulesResource = "modules.kmm.sigs.x-k8s.io"
	requiredOCPVersion = "4.21"

	spectrumScaleCSIProvisioner = "spectrumscale.csi.ibm.com"

	gpfsContainerName = "gpfs"

	// Phase 6: mmgetstate gate — after daemon pod restarts, wait before rechecking.
	gpfsClusterActiveWaitTimeout        = 40 * time.Minute
	gpfsClusterPostDaemonRestartWait    = 5 * time.Minute
	gpfsClusterExecPodRetryInterval     = 30 * time.Second
)

var (
	subscriptionGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "subscriptions",
	}
	csvGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions",
	}
	operatorGroupGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1", Resource: "operatorgroups",
	}
	clusterVersionGVR = schema.GroupVersionResource{
		Group: "config.openshift.io", Version: "v1", Resource: "clusterversions",
	}
	catalogSourceGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "catalogsources",
	}
	// API for oc get modules.kmm.sigs.x-k8s.io (-n <namespace>)
	kmmModuleGVR = schema.GroupVersionResource{
		Group: "kmm.sigs.x-k8s.io", Version: "v1beta1", Resource: "modules",
	}
)
