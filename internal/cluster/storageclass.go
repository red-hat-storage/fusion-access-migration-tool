package cluster

import (
	"fmt"

	"github.com/red-hat-storage/fusion-access-migration-tool/internal/constants"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/helpers"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/kube"
	"github.com/red-hat-storage/fusion-access-migration-tool/internal/output"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func ListSpectrumScaleStorageClasses(mc *kube.Context) error {
	list, err := mc.Clientset.StorageV1().StorageClasses().List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StorageClasses: %w", err)
	}

	var names []string
	for _, sc := range list.Items {
		if sc.Provisioner == constants.SpectrumScaleCSIProvisioner {
			names = append(names, sc.Name)
		}
	}

	if len(names) == 0 {
		output.PrintSkip(fmt.Sprintf("No StorageClasses with provisioner %s", constants.SpectrumScaleCSIProvisioner))
		return nil
	}

	output.PrintInfo(fmt.Sprintf("StorageClasses (provisioner %s):", constants.SpectrumScaleCSIProvisioner))
	for _, n := range names {
		output.PrintInfo(fmt.Sprintf("  %s", n))
	}
	output.PrintSuccess(fmt.Sprintf("Listed %d StorageClass(es)", len(names)))
	return nil
}

func spectrumScaleSANStorageClass(name, volBackendFs string, vmDisk bool) *storagev1.StorageClass {
	params := map[string]string{
		"filesetType":  "independent",
		"volBackendFs": volBackendFs,
	}
	if vmDisk {
		params["volumeType"] = "vmdisk"
	}
	binding := storagev1.VolumeBindingImmediate
	return &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: name},
		Provisioner:          constants.SpectrumScaleCSIProvisioner,
		Parameters:           params,
		ReclaimPolicy:        ptr.To(corev1.PersistentVolumeReclaimDelete),
		AllowVolumeExpansion: ptr.To(true),
		VolumeBindingMode:    &binding,
	}
}

func ensureStorageClass(mc *kube.Context, sc *storagev1.StorageClass) error {
	_, err := mc.Clientset.StorageV1().StorageClasses().Get(mc.Ctx, sc.Name, metav1.GetOptions{})
	if err == nil {
		if mc.ResumingFromCheckpoint {
			output.PrintSkip(fmt.Sprintf("StorageClass %s already exists", sc.Name))
			return nil
		}
		return fmt.Errorf("additional StorageClass %q already exists — remove it or ensure migration resumes from checkpoint so the existing class is accepted", sc.Name)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get StorageClass %s: %w", sc.Name, err)
	}
	if mc.DryRun {
		output.PrintDryRun(fmt.Sprintf("Would create StorageClass %s", sc.Name))
		return nil
	}
	if _, err := mc.Clientset.StorageV1().StorageClasses().Create(mc.Ctx, sc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create StorageClass %s: %w", sc.Name, err)
	}
	output.PrintSuccess(fmt.Sprintf("Created StorageClass %s", sc.Name))
	return nil
}

// EnsureSANStorageClassesForScaleFilesystems creates san-{fs} and san-{fs}-vm StorageClasses per filesystems.scale CR (metadata.name).
func EnsureSANStorageClassesForScaleFilesystems(mc *kube.Context) error {
	gvr := helpers.ParseGVR(constants.FilesystemResource)
	fsList, err := mc.Dynamic.Resource(gvr).Namespace(constants.SpectrumScaleNS).List(mc.Ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list filesystems: %w", err)
	}
	if len(fsList.Items) == 0 {
		output.PrintWarning("No filesystems found — skipping SAN StorageClass creation")
		return nil
	}
	for _, fs := range fsList.Items {
		fsName := fs.GetName()
		generalName := fmt.Sprintf("san-%s", fsName)
		vmName := fmt.Sprintf("san-%s-vm", fsName)
		if err := ensureStorageClass(mc, spectrumScaleSANStorageClass(generalName, fsName, false)); err != nil {
			return err
		}
		if err := ensureStorageClass(mc, spectrumScaleSANStorageClass(vmName, fsName, true)); err != nil {
			return err
		}
	}
	return nil
}
