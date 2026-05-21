package cluster

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestOperatorGroupTargetsNamespace(t *testing.T) {
	target := constants.FusionOperatorNS
	tests := []struct {
		name    string
		og      *unstructured.Unstructured
		want    bool
		wantErr bool
	}{
		{
			name: "explicit target matches",
			og: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "custom-og", "namespace": constants.FusionOperatorNS,
					},
					"spec": map[string]interface{}{
						"targetNamespaces": []interface{}{constants.FusionOperatorNS},
					},
				},
			},
			want: true,
		},
		{
			name: "explicit list includes fusion among others",
			og: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "og", "namespace": constants.FusionOperatorNS,
					},
					"spec": map[string]interface{}{
						"targetNamespaces": []interface{}{"other-ns", constants.FusionOperatorNS},
					},
				},
			},
			want: true,
		},
		{
			name: "explicit targets omit fusion",
			og: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "og", "namespace": constants.FusionOperatorNS,
					},
					"spec": map[string]interface{}{
						"targetNamespaces": []interface{}{"other-ns"},
					},
				},
			},
			want: false,
		},
		{
			name: "empty targetNamespaces uses operatorgroup namespace",
			og: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "og", "namespace": constants.FusionOperatorNS,
					},
					"spec": map[string]interface{}{
						"targetNamespaces": []interface{}{},
					},
				},
			},
			want: true,
		},
		{
			name: "missing spec uses own namespace",
			og: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "og", "namespace": constants.FusionOperatorNS,
					},
				},
			},
			want: true,
		},
		{
			name: "wrong namespace with empty targets",
			og: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "og", "namespace": "other-ns",
					},
					"spec": map[string]interface{}{
						"targetNamespaces": []interface{}{},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := operatorGroupTargetsNamespace(tt.og, target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("operatorGroupTargetsNamespace = %v, want %v", got, tt.want)
			}
		})
	}
}

func newFusionOperatorTestContext(cs kubernetes.Interface, dynObjs ...*unstructured.Unstructured) *kube.Context {
	if cs == nil {
		cs = kubefake.NewSimpleClientset()
	}
	customListKinds := map[schema.GroupVersionResource]string{
		constants.OperatorGroupGVR: "OperatorGroupList",
	}
	runtimeObjs := make([]runtime.Object, 0, len(dynObjs))
	for _, o := range dynObjs {
		runtimeObjs = append(runtimeObjs, o)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		customListKinds,
		runtimeObjs...,
	)
	return &kube.Context{
		Ctx:       context.Background(),
		Clientset: cs,
		Dynamic:   dyn,
	}
}

func coreNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func testFusionOperatorGroup(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.FusionOperatorNS,
			},
			"spec": map[string]interface{}{
				"targetNamespaces": []interface{}{constants.FusionOperatorNS},
				"upgradeStrategy":  "Default",
			},
		},
	}
}

func testFusionCatalogSource() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "CatalogSource",
			"metadata": map[string]interface{}{
				"name":      constants.FusionOperatorCatalogSourceName,
				"namespace": constants.OpenShiftMarketplaceNS,
			},
			"spec": map[string]interface{}{
				"image": "preinstalled.example/catalog:1",
			},
		},
	}
}

func testFusionSubscription(withCurrentCSV bool) *unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind":       "Subscription",
		"metadata": map[string]interface{}{
			"name":      constants.FusionOperatorSubscriptionName,
			"namespace": constants.FusionOperatorNS,
		},
		"spec": map[string]interface{}{
			"channel":             constants.FusionOperatorSubscriptionChannel,
			"name":                constants.FusionOperatorSubscriptionName,
			"source":              constants.FusionOperatorCatalogSourceName,
			"sourceNamespace":     constants.OpenShiftMarketplaceNS,
			"installPlanApproval": "Automatic",
		},
	}
	if withCurrentCSV {
		obj["status"] = map[string]interface{}{
			"currentCSV": "isf-operator.v2.0.0-test",
		}
	}
	return &unstructured.Unstructured{Object: obj}
}

func testFusionCSVSucceeded(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": constants.FusionOperatorNS,
			},
			"status": map[string]interface{}{
				"phase": "Succeeded",
			},
		},
	}
}

func TestEnsureFusionOperatorOLMInstall_createsWhenMissing(t *testing.T) {
	catalogImage := "icr.io/example/ibm-operator-catalog:test"
	mc := newFusionOperatorTestContext(nil)
	mc.FusionOperatorCatalogImage = catalogImage
	mc.DryRun = false

	if err := ensureFusionOperatorOLMInstall(mc); err != nil {
		t.Fatalf("ensureFusionOperatorOLMInstall: %v", err)
	}

	if _, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionOperatorNS, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected namespace %s: %v", constants.FusionOperatorNS, err)
	}

	og, err := mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.FusionOperatorNS).Get(
		mc.Ctx, constants.FusionOperatorGroupName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get OperatorGroup: %v", err)
	}
	tns, _, _ := unstructured.NestedStringSlice(og.Object, "spec", "targetNamespaces")
	if len(tns) != 1 || tns[0] != constants.FusionOperatorNS {
		t.Fatalf("unexpected OperatorGroup targetNamespaces: %v", tns)
	}

	cs, err := mc.Dynamic.Resource(constants.CatalogSourceGVR).Namespace(constants.OpenShiftMarketplaceNS).Get(
		mc.Ctx, constants.FusionOperatorCatalogSourceName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get CatalogSource: %v", err)
	}
	img, _, _ := unstructured.NestedString(cs.Object, "spec", "image")
	if img != catalogImage {
		t.Fatalf("CatalogSource spec.image = %q, want %q", img, catalogImage)
	}

	sub, err := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.FusionOperatorNS).Get(
		mc.Ctx, constants.FusionOperatorSubscriptionName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Subscription: %v", err)
	}
	ch, _, _ := unstructured.NestedString(sub.Object, "spec", "channel")
	if ch != constants.FusionOperatorSubscriptionChannel {
		t.Fatalf("Subscription channel = %q", ch)
	}
	src, _, _ := unstructured.NestedString(sub.Object, "spec", "source")
	if src != constants.FusionOperatorCatalogSourceName {
		t.Fatalf("Subscription source = %q", src)
	}
}

func TestEnsureFusionOperatorOLMInstall_skipsWhenResourcesExist(t *testing.T) {
	cs := kubefake.NewSimpleClientset(coreNamespace(constants.FusionOperatorNS))
	og := testFusionOperatorGroup(constants.FusionOperatorGroupName)
	cat := testFusionCatalogSource()
	sub := testFusionSubscription(false)
	mc := newFusionOperatorTestContext(cs, og, cat, sub)
	mc.FusionOperatorCatalogImage = "icr.io/other/catalog:latest"
	mc.DryRun = false

	if err := ensureFusionOperatorOLMInstall(mc); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := ensureFusionOperatorOLMInstall(mc); err != nil {
		t.Fatalf("second run (idempotent): %v", err)
	}

	// Catalog image unchanged (skip path does not update existing CatalogSource)
	csOut, err := mc.Dynamic.Resource(constants.CatalogSourceGVR).Namespace(constants.OpenShiftMarketplaceNS).Get(
		mc.Ctx, constants.FusionOperatorCatalogSourceName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	img, _, _ := unstructured.NestedString(csOut.Object, "spec", "image")
	if img != "preinstalled.example/catalog:1" {
		t.Fatalf("expected existing catalog image unchanged, got %q", img)
	}
}

func TestEnsureFusionOperatorOLMInstall_dryRun(t *testing.T) {
	mc := newFusionOperatorTestContext(nil)
	mc.DryRun = true
	mc.FusionOperatorCatalogImage = "icr.io/example/catalog:latest"

	if err := ensureFusionOperatorOLMInstall(mc); err != nil {
		t.Fatalf("ensureFusionOperatorOLMInstall dry-run: %v", err)
	}

	_, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionOperatorNS, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected fusion namespace absent after dry-run, err=%v", err)
	}

	list, err := mc.Dynamic.Resource(constants.OperatorGroupGVR).Namespace(constants.FusionOperatorNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list OperatorGroups: %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected no OperatorGroups in tracker, got %d", len(list.Items))
	}
}

func TestEnsureFusionOperatorResources_dryRun(t *testing.T) {
	mc := newFusionOperatorTestContext(nil)
	mc.DryRun = true
	mc.FusionOperatorCatalogImage = "icr.io/example/catalog:latest"

	if err := EnsureFusionOperatorResources(mc); err != nil {
		t.Fatalf("EnsureFusionOperatorResources dry-run: %v", err)
	}

	_, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionOperatorNS, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected fusion namespace absent after full dry-run, err=%v", err)
	}
}

func TestEnsureFusionOperatorOLMInstall_apiErrorOnOperatorGroupList(t *testing.T) {
	mc := newFusionOperatorTestContext(nil)
	mc.FusionOperatorCatalogImage = "icr.io/x:latest"
	mc.DryRun = false

	fdc := mc.Dynamic.(*dynamicfake.FakeDynamicClient)
	fdc.PrependReactor("list", "operatorgroups", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("injected list failure")
	})

	err := ensureFusionOperatorOLMInstall(mc)
	if err == nil {
		t.Fatal("expected error from OperatorGroup list")
	}
	if !strings.Contains(err.Error(), "list OperatorGroups") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureFusionOperatorOLMInstall_apiErrorOnCatalogSourceGet(t *testing.T) {
	cs := kubefake.NewSimpleClientset(coreNamespace(constants.FusionOperatorNS))
	mc := newFusionOperatorTestContext(cs, testFusionOperatorGroup(constants.FusionOperatorGroupName))
	mc.FusionOperatorCatalogImage = "icr.io/x:latest"
	mc.DryRun = false

	fdc := mc.Dynamic.(*dynamicfake.FakeDynamicClient)
	fdc.PrependReactor("get", "catalogsources", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("injected catalog get failure")
	})

	err := ensureFusionOperatorOLMInstall(mc)
	if err == nil {
		t.Fatal("expected error from CatalogSource get")
	}
	if !strings.Contains(err.Error(), "get CatalogSource") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForFusionOperatorCSVSucceeded_success(t *testing.T) {
	csvName := "isf-operator.v2.0.0-test"
	mc := newFusionOperatorTestContext(
		kubefake.NewSimpleClientset(coreNamespace(constants.FusionOperatorNS)),
		testFusionSubscription(true),
		testFusionCSVSucceeded(csvName),
	)
	mc.FusionOperatorCSVWaitTimeout = 200 * time.Millisecond
	mc.FusionOperatorCSVWaitPollInterval = 10 * time.Millisecond
	mc.DryRun = false

	if err := waitForFusionOperatorCSVSucceeded(mc); err != nil {
		t.Fatalf("waitForFusionOperatorCSVSucceeded: %v", err)
	}
}

func TestWaitForFusionOperatorCSVSucceeded_getSubscriptionError(t *testing.T) {
	mc := newFusionOperatorTestContext(nil)
	mc.FusionOperatorCSVWaitTimeout = 100 * time.Millisecond
	mc.FusionOperatorCSVWaitPollInterval = 10 * time.Millisecond
	mc.DryRun = false

	fdc := mc.Dynamic.(*dynamicfake.FakeDynamicClient)
	fdc.PrependReactor("get", "subscriptions", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("injected subscription get failure")
	})

	err := waitForFusionOperatorCSVSucceeded(mc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get Subscription") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForFusionOperatorCSVSucceeded_timesOutWithoutCurrentCSV(t *testing.T) {
	mc := newFusionOperatorTestContext(
		kubefake.NewSimpleClientset(coreNamespace(constants.FusionOperatorNS)),
		testFusionSubscription(false),
	)
	mc.FusionOperatorCSVWaitTimeout = 50 * time.Millisecond
	mc.FusionOperatorCSVWaitPollInterval = 15 * time.Millisecond
	mc.DryRun = false

	err := waitForFusionOperatorCSVSucceeded(mc)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "did not reach Succeeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}
