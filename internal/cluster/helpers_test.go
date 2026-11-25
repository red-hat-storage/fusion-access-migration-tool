package cluster

import (
	"context"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

// testListKinds is the union of every list kind used by tests in this package. The fake dynamic
// client requires a list kind for any resource a test LISTs; registering kinds a given test does
// not use is harmless, so one shared map avoids a per-file constructor.
var testListKinds = map[schema.GroupVersionResource]string{
	constants.SubscriptionGVR:    "SubscriptionList",
	constants.CsvGVR:             "ClusterServiceVersionList",
	constants.CatalogSourceGVR:   "CatalogSourceList",
	constants.OperatorGroupGVR:   "OperatorGroupList",
	constants.OdfManagerGVR:      "OdfManagerList",
	constants.PackageManifestGVR: "PackageManifestList",

	{Group: "scale.spectrum.ibm.com", Version: "v1beta1", Resource: "clusters"}:    "ClusterList",
	{Group: "scale.spectrum.ibm.com", Version: "v1beta1", Resource: "filesystems"}: "FilesystemList",
	{Group: "scale.spectrum.ibm.com", Version: "v1beta1", Resource: "localdisks"}:  "LocalDiskList",
}

// newClusterTestContext builds a kube.Context backed by fake clients. Pass nil for cs to get an
// empty clientset; pass one explicitly when the test needs seeded objects or a reactor.
func newClusterTestContext(cs kubernetes.Interface, dynObjs ...*unstructured.Unstructured) *kube.Context {
	if cs == nil {
		cs = kubefake.NewSimpleClientset()
	}
	runtimeObjs := make([]runtime.Object, 0, len(dynObjs))
	for _, obj := range dynObjs {
		runtimeObjs = append(runtimeObjs, obj)
	}
	return &kube.Context{
		Ctx:       context.Background(),
		Clientset: cs,
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
			runtime.NewScheme(),
			testListKinds,
			runtimeObjs...,
		),
	}
}
