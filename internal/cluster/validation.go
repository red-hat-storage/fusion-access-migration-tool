package cluster

import (
	"fmt"
	"strings"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
)

func ValidateClusterConnectivity(mc *kube.Context) error {
	version, err := mc.Clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("not logged in to OpenShift cluster: %w", err)
	}

	config, _ := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	currentContext := config.CurrentContext

	output.PrintInfo(fmt.Sprintf("Cluster: %s", currentContext))
	if version != nil {
		output.PrintInfo(fmt.Sprintf("Server version: %s", version.GitVersion))
	}
	output.PrintInfo(fmt.Sprintf("Namespaces: %s, %s, %s, %s",
		constants.FusionAccessNS, constants.SpectrumScaleNS, constants.SpectrumScaleOperatorNS, constants.OpenShiftStorageNS))
	output.PrintSuccess("Cluster connectivity verified")
	return nil
}

func ValidateOCPVersion(mc *kube.Context) error {
	cv, err := mc.Dynamic.Resource(constants.ClusterVersionGVR).Get(mc.Ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ClusterVersion: %w", err)
	}
	histories, found, _ := unstructured.NestedSlice(cv.Object, "status", "history")
	if !found || len(histories) == 0 {
		return fmt.Errorf("no version history found in ClusterVersion")
	}
	entry, ok := histories[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected format in ClusterVersion history")
	}
	currentVersion, _, _ := unstructured.NestedString(entry, "version")
	if !strings.HasPrefix(currentVersion, constants.RequiredOCPVersion) {
		return fmt.Errorf("OCP version %s does not match required %s.x", currentVersion, constants.RequiredOCPVersion)
	}
	output.PrintSuccess(fmt.Sprintf("OCP version %s meets requirement (%s.x)", currentVersion, constants.RequiredOCPVersion))
	return nil
}

func ValidateBasicClusterHealth(mc *kube.Context) error {
	cv, err := mc.Dynamic.Resource(constants.ClusterVersionGVR).Get(mc.Ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ClusterVersion for health validation: %w", err)
	}

	conditions, found, err := unstructured.NestedSlice(cv.Object, "status", "conditions")
	if err != nil {
		return fmt.Errorf("failed to parse ClusterVersion status.conditions: %w", err)
	}
	if !found {
		return fmt.Errorf("ClusterVersion status.conditions not found")
	}

	failingStatus, failingMsg := clusterVersionConditionStatus(conditions, "Failing")
	degradedStatus, degradedMsg := clusterVersionConditionStatus(conditions, "Degraded")
	if strings.EqualFold(failingStatus, "True") || strings.EqualFold(degradedStatus, "True") {
		return fmt.Errorf(
			"cluster is not healthy: Failing=%s (%s), Degraded=%s (%s)",
			emptyConditionStatus(failingStatus), emptyConditionMessage(failingMsg),
			emptyConditionStatus(degradedStatus), emptyConditionMessage(degradedMsg),
		)
	}

	output.PrintSuccess("Basic OCP cluster health validated (ClusterVersion is not failing/degraded)")
	return nil
}

func clusterVersionConditionStatus(conditions []interface{}, conditionType string) (string, string) {
	for _, cond := range conditions {
		m, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _, _ := unstructured.NestedString(m, "type")
		if typ != conditionType {
			continue
		}
		status, _, _ := unstructured.NestedString(m, "status")
		message, _, _ := unstructured.NestedString(m, "message")
		reason, _, _ := unstructured.NestedString(m, "reason")
		if reason != "" && message != "" {
			return status, fmt.Sprintf("%s: %s", reason, message)
		}
		if reason != "" {
			return status, reason
		}
		return status, message
	}
	return "", ""
}

func emptyConditionStatus(status string) string {
	if status == "" {
		return "Unknown"
	}
	return status
}

func emptyConditionMessage(msg string) string {
	if msg == "" {
		return "n/a"
	}
	return msg
}

func ValidateScaleClusterExists(mc *kube.Context) error {
	clusterGVR, err := helpers.ResolveGVR(
		mc.Ctx,
		mc.Dynamic,
		"scale.spectrum.ibm.com",
		"clusters",
		[]string{"v1beta1", "v1"},
	)
	if err != nil {
		return fmt.Errorf("failed to resolve Scale cluster API: %w", err)
	}
	clusterList, err := mc.Dynamic.Resource(clusterGVR).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Scale clusters: %w", err)
	}
	if len(clusterList.Items) == 0 {
		return fmt.Errorf("no Scale clusters found in namespace %s", constants.SpectrumScaleNS)
	}

	output.PrintSuccess(fmt.Sprintf("Scale cluster exists (%d found)", len(clusterList.Items)))
	return nil
}

func ValidateScaleFilesystemHealthIfPresent(mc *kube.Context) error {
	gvr := helpers.ParseGVR(constants.FilesystemResource)
	fsList, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list filesystems in %s: %w", constants.SpectrumScaleNS, err)
	}
	if len(fsList.Items) == 0 {
		output.PrintInfo(fmt.Sprintf("No filesystems found in namespace %s; skipping filesystem health validation", constants.SpectrumScaleNS))
		return nil
	}

	var unhealthy []string
	for _, fs := range fsList.Items {
		name := fs.GetName()
		phase, _, _ := unstructured.NestedString(fs.Object, "status", "phase")
		mounted, _, _ := unstructured.NestedBool(fs.Object, "status", "mounted")
		if phase == "" {
			phase = "Unknown"
		}
		output.PrintInfo(fmt.Sprintf("Filesystem %s: phase=%s, mounted=%v", name, phase, mounted))
		if !mounted {
			unhealthy = append(unhealthy, fmt.Sprintf("%s (phase=%s, mounted=%v)", name, phase, mounted))
		}
	}
	if len(unhealthy) > 0 {
		return fmt.Errorf("filesystem health validation failed: %s", strings.Join(unhealthy, ", "))
	}

	output.PrintSuccess(fmt.Sprintf("Filesystem health validated (%d filesystem resources checked)", len(fsList.Items)))
	return nil
}

func ValidateLocalDisksReadyIfPresent(mc *kube.Context) error {
	gvr := helpers.ParseGVR(constants.LocalDiskResource)
	diskList, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list localdisks in %s: %w", constants.SpectrumScaleNS, err)
	}
	if len(diskList.Items) == 0 {
		output.PrintInfo(fmt.Sprintf("No localdisks found in namespace %s; skipping localdisk readiness validation", constants.SpectrumScaleNS))
		return nil
	}

	var unhealthy []string
	for _, disk := range diskList.Items {
		ready, detail := localDiskReady(&disk)
		if ready {
			output.PrintInfo(fmt.Sprintf("LocalDisk %s: %s", disk.GetName(), detail))
			continue
		}
		unhealthy = append(unhealthy, fmt.Sprintf("%s (%s)", disk.GetName(), detail))
	}
	if len(unhealthy) > 0 {
		return fmt.Errorf("localdisk readiness validation failed: %s", strings.Join(unhealthy, ", "))
	}

	output.PrintSuccess(fmt.Sprintf("LocalDisk readiness validated (%d localdisk resources checked)", len(diskList.Items)))
	return nil
}

func localDiskReady(disk *unstructured.Unstructured) (bool, string) {
	phase, _, _ := unstructured.NestedString(disk.Object, "status", "phase")
	state, _, _ := unstructured.NestedString(disk.Object, "status", "state")
	if strings.EqualFold(phase, "Ready") {
		return true, fmt.Sprintf("phase=%s", phase)
	}
	if strings.EqualFold(state, "Ready") {
		return true, fmt.Sprintf("state=%s", state)
	}

	conditions, found, _ := unstructured.NestedSlice(disk.Object, "status", "conditions")
	if found {
		for _, cond := range conditions {
			m, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _, _ := unstructured.NestedString(m, "type")
			status, _, _ := unstructured.NestedString(m, "status")
			if strings.EqualFold(typ, "Ready") && strings.EqualFold(status, "True") {
				return true, "condition Ready=True"
			}
			if (strings.EqualFold(typ, "Failed") || strings.EqualFold(typ, "Degraded")) && strings.EqualFold(status, "True") {
				return false, fmt.Sprintf("condition %s=True", typ)
			}
		}
	}

	if phase != "" {
		return false, fmt.Sprintf("phase=%s", phase)
	}
	if state != "" {
		return false, fmt.Sprintf("state=%s", state)
	}
	return false, "not Ready (missing status.phase/state/conditions)"
}

func ValidateExistingInstalls(mc *kube.Context) error {
	_, nsErr := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.OpenShiftStorageNS, metav1.GetOptions{})
	if nsErr != nil {
		if apierrors.IsNotFound(nsErr) {
			output.PrintInfo(fmt.Sprintf("Namespace %s not present; skipping ODF/FDF subscription checks (created when installing FDF)", constants.OpenShiftStorageNS))
			output.PrintSuccess("No conflicting FDF installation found")
			return nil
		}
		return fmt.Errorf("failed to get namespace %s: %w", constants.OpenShiftStorageNS, nsErr)
	}

	subs, err := mc.Dynamic.Resource(constants.SubscriptionGVR).Namespace(constants.OpenShiftStorageNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list subscriptions in %s: %w", constants.OpenShiftStorageNS, err)
	}

	var odfSubs []unstructured.Unstructured
	for i := range subs.Items {
		if strings.HasPrefix(subs.Items[i].GetName(), constants.OdfOperatorSubPrefix) {
			odfSubs = append(odfSubs, subs.Items[i])
		}
	}

	if len(odfSubs) == 0 {
		output.PrintSuccess("No conflicting FDF installation found")
		return nil
	}

	for _, sub := range odfSubs {
		name := sub.GetName()
		output.PrintInfo(fmt.Sprintf("Found existing ODF subscription: %s", name))

		csvName, ok := helpers.SubscriptionCurrentCSV(&sub)
		if !ok {
			return fmt.Errorf(
				"odf-operator subscription %q in %s has no status.currentCSV yet; wait until OLM resolves the CSV before migration preflight",
				name, constants.OpenShiftStorageNS,
			)
		}

		csv, err := helpers.GetClusterServiceVersion(mc.Ctx, mc.Dynamic, constants.OpenShiftStorageNS, csvName)
		if err != nil {
			return fmt.Errorf("get CSV %q in %s: %w", csvName, constants.OpenShiftStorageNS, err)
		}

		provider := helpers.CSVSpecProviderName(csv)
		specVersion := helpers.CSVSpecVersion(csv)

		switch provider {
		case constants.OdfProviderIBM:
			if err := FdfOdfPreflightAllowed(specVersion, mc.ResumingFromCheckpoint); err != nil {
				return fmt.Errorf("odf-operator CSV %q (version %s): %w", csvName, specVersion, err)
			}
			output.PrintInfo(fmt.Sprintf(
				"IBM FDF odf-operator CSV %q version %s passed preflight",
				csvName, specVersion,
			))
		case constants.OdfProviderRedHat:
			return fmt.Errorf(
				"odf-operator CSV %q has provider %q; only IBM FDF 4.20.x is supported to start migration",
				csvName, provider,
			)
		default:
			if provider == "" {
				return fmt.Errorf("odf-operator CSV %q has no spec.provider.name; cannot validate", csvName)
			}
			return fmt.Errorf(
				"odf-operator CSV %q has unsupported provider %q; only IBM FDF 4.20.x is supported to start migration",
				csvName, provider,
			)
		}
	}

	output.PrintSuccess("Existing ODF/FDF installation validated for migration")
	return nil
}

func ValidateRequiredNamespaces(mc *kube.Context) error {
	_, err := mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.SpectrumScaleNS, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("namespace '%s' does not exist: %w", constants.SpectrumScaleNS, err)
	}

	_, err = mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.SpectrumScaleOperatorNS, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf(
				"namespace %q not found — required for a new migration; when resuming, preflight is skipped if migration state in the ConfigMap shows progress (see README)",
				constants.SpectrumScaleOperatorNS,
			)
		}
		return fmt.Errorf("namespace '%s': %w", constants.SpectrumScaleOperatorNS, err)
	}

	_, err = mc.Clientset.CoreV1().Namespaces().Get(mc.Ctx, constants.FusionAccessNS, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf(
				"namespace %q not found — required for a new migration; when resuming, preflight is skipped if migration state in the ConfigMap shows progress (see README)",
				constants.FusionAccessNS,
			)
		}
		return fmt.Errorf("namespace '%s': %w", constants.FusionAccessNS, err)
	}

	output.PrintSuccess("Required namespaces validated")
	return nil
}

func ValidateSecureBootSigningSecrets(mc *kube.Context) error {
	_, errKey := mc.Clientset.CoreV1().Secrets(constants.FusionAccessNS).Get(
		mc.Ctx, constants.SecureBootSigningKeySecret, metav1.GetOptions{},
	)
	_, errPub := mc.Clientset.CoreV1().Secrets(constants.FusionAccessNS).Get(
		mc.Ctx, constants.SecureBootSigningKeyPubSecret, metav1.GetOptions{},
	)

	keyMissing := apierrors.IsNotFound(errKey)
	pubMissing := apierrors.IsNotFound(errPub)

	if keyMissing && pubMissing {
		output.PrintInfo(fmt.Sprintf(
			"Secure boot signing secrets %q and %q not found in %s — cluster treated as non-secure-boot",
			constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret, constants.FusionAccessNS,
		))
		return nil
	}

	if keyMissing != pubMissing {
		return fmt.Errorf(
			"incomplete secure boot signing secret setup in %s: expected both %q and %q, but only one exists",
			constants.FusionAccessNS, constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret,
		)
	}

	if errKey != nil {
		return fmt.Errorf("get secret %s/%s: %w", constants.FusionAccessNS, constants.SecureBootSigningKeySecret, errKey)
	}
	if errPub != nil {
		return fmt.Errorf("get secret %s/%s: %w", constants.FusionAccessNS, constants.SecureBootSigningKeyPubSecret, errPub)
	}

	output.PrintSuccess(fmt.Sprintf(
		"Secure boot cluster detected: secrets %q and %q found in %s",
		constants.SecureBootSigningKeySecret, constants.SecureBootSigningKeyPubSecret, constants.FusionAccessNS,
	))
	return nil
}
