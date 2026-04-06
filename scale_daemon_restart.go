// scale_daemon_restart.go — Phase 6 blocks completion until mmgetstate -a shows every
// GPFS node as active. If any node is down, deletes all worker GPFS daemon pods, waits
// 5 minutes, then runs mmgetstate again. Pod name matches node short hostname.
package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

func podHasGpfsContainer(p *corev1.Pod) bool {
	for _, c := range p.Spec.Containers {
		if c.Name == gpfsContainerName {
			return true
		}
	}
	return false
}

// workerNodeShortNames returns the first DNS label of each non-control-plane Node
// (e.g. ip-10-0-4-41 from ip-10-0-4-41.ap-south-2.compute.internal).
func workerNodeShortNames(mc *MigrationContext) (map[string]struct{}, error) {
	list, err := mc.clientset.CoreV1().Nodes().List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{})
	for _, n := range list.Items {
		if n.Labels["node-role.kubernetes.io/control-plane"] == "true" ||
			n.Labels["node-role.kubernetes.io/master"] == "true" {
			continue
		}
		short := strings.SplitN(n.Name, ".", 2)[0]
		if short != "" {
			out[short] = struct{}{}
		}
	}
	return out, nil
}

// findGpfsPodOnWorkerNode picks a Running pod in ibm-spectrum-scale whose name equals
// a worker node short hostname and has the gpfs container (Spectrum Scale daemon pod).
func findGpfsPodOnWorkerNode(mc *MigrationContext, workers map[string]struct{}) (string, error) {
	pods, err := mc.clientset.CoreV1().Pods(spectrumScaleNS).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		if _, ok := workers[p.Name]; !ok {
			continue
		}
		if !podHasGpfsContainer(&p) {
			continue
		}
		candidates = append(candidates, p.Name)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no Running GPFS pod in %s named like a worker node (with container %q)", spectrumScaleNS, gpfsContainerName)
	}
	sort.Strings(candidates)
	return candidates[0], nil
}

func execInGpfsPod(mc *MigrationContext, podName string, command []string) (stdout, stderr string, err error) {
	req := mc.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(spectrumScaleNS).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: gpfsContainerName,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	u := req.URL()
	exec, err := remotecommand.NewSPDYExecutor(mc.restConfig, "POST", u)
	if err != nil {
		return "", "", err
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	err = exec.StreamWithContext(mc.ctx, remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})
	return stdoutBuf.String(), stderrBuf.String(), err
}

// parseMMGetStateNonActiveSummaries parses `mmgetstate -a` table output; returns one entry
// per GPFS node whose state is not "active" (e.g. "ip-10-0-4-41 (down)"), skipping headers.
func parseMMGetStateNonActiveSummaries(output string) []string {
	var bad []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		state := fields[len(fields)-1]
		node := fields[len(fields)-2]
		if strings.EqualFold(node, "name") || strings.EqualFold(state, "state") {
			continue
		}
		if strings.EqualFold(state, "active") {
			continue
		}
		bad = append(bad, fmt.Sprintf("%s (%s)", node, state))
	}
	return bad
}

// parseMMGetStateDownNodes parses `mmgetstate -a` output; returns GPFS node names whose
// state is "down" (same column layout as parseMMGetStateNonActiveSummaries).
func parseMMGetStateDownNodes(output string) []string {
	var down []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		state := fields[len(fields)-1]
		node := fields[len(fields)-2]
		if strings.EqualFold(node, "name") || strings.EqualFold(state, "state") {
			continue
		}
		if strings.EqualFold(state, "down") {
			down = append(down, node)
		}
	}
	return down
}

// listGpfsWorkerDaemonPods returns Running GPFS pods in ibm-spectrum-scale whose name
// matches a worker node short hostname.
func listGpfsWorkerDaemonPods(mc *MigrationContext, workers map[string]struct{}) ([]corev1.Pod, error) {
	list, err := mc.clientset.CoreV1().Pods(spectrumScaleNS).List(mc.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for _, p := range list.Items {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		if _, ok := workers[p.Name]; !ok {
			continue
		}
		if !podHasGpfsContainer(&p) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// deleteAllGpfsWorkerDaemonPods deletes every Running GPFS worker daemon pod in
// ibm-spectrum-scale (name matches a worker node, gpfs container present).
func deleteAllGpfsWorkerDaemonPods(mc *MigrationContext, workers map[string]struct{}) (deleted int, err error) {
	daemonPods, err := listGpfsWorkerDaemonPods(mc, workers)
	if err != nil {
		return 0, fmt.Errorf("list GPFS worker daemon pods: %w", err)
	}
	for _, p := range daemonPods {
		if err := mc.clientset.CoreV1().Pods(spectrumScaleNS).Delete(mc.ctx, p.Name, metav1.DeleteOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return deleted, fmt.Errorf("delete pod %s: %w", p.Name, err)
		}
		printSuccess(fmt.Sprintf("Deleted GPFS daemon pod %s", p.Name))
		deleted++
	}
	return deleted, nil
}

// waitForGPFSAllNodesActive polls mmgetstate -a until every node is active or the wait
// times out. If any node is down, deletes all worker daemon pods, waits 5 minutes, then
// rechecks; other non-active states wait 5 minutes before recheck without deleting.
func waitForGPFSAllNodesActive(mc *MigrationContext) error {
	if _, err := mc.clientset.CoreV1().Namespaces().Get(mc.ctx, spectrumScaleNS, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			printSkip(fmt.Sprintf("Namespace %s not found — skipping GPFS mmgetstate gate", spectrumScaleNS))
			return nil
		}
		return fmt.Errorf("check namespace %s: %w", spectrumScaleNS, err)
	}

	workers, err := workerNodeShortNames(mc)
	if err != nil {
		return fmt.Errorf("list worker nodes: %w", err)
	}
	if len(workers) == 0 {
		return fmt.Errorf("no worker nodes: cannot run mmgetstate -a gate for GPFS")
	}

	if dryRun {
		printDryRun(fmt.Sprintf(
			"Would poll mmgetstate -a (via a Running GPFS pod in %s with container %s) until all nodes are active (timeout %v); on node down would delete all worker daemon pods in %s then wait %v before recheck",
			spectrumScaleNS, gpfsContainerName, gpfsClusterActiveWaitTimeout, spectrumScaleNS, gpfsClusterPostDaemonRestartWait,
		))
		return nil
	}

	deadline := time.Now().Add(gpfsClusterActiveWaitTimeout)
	var lastNonActive []string

	for {
		if time.Now().After(deadline) {
			msg := fmt.Sprintf(
				"timed out after %v waiting for all GPFS nodes to be active (mmgetstate -a)",
				gpfsClusterActiveWaitTimeout,
			)
			if len(lastNonActive) > 0 {
				msg += fmt.Sprintf("; last non-active: %s", strings.Join(lastNonActive, ", "))
			}
			return fmt.Errorf(msg)
		}

		select {
		case <-mc.ctx.Done():
			return mc.ctx.Err()
		default:
		}

		podName, ferr := findGpfsPodOnWorkerNode(mc, workers)
		if ferr != nil {
			printInfo(fmt.Sprintf("Waiting for a Running GPFS worker pod to run mmgetstate: %v", ferr))
			select {
			case <-mc.ctx.Done():
				return mc.ctx.Err()
			case <-time.After(gpfsClusterExecPodRetryInterval):
			}
			continue
		}

		stdout, stderr, err := execInGpfsPod(mc, podName, []string{"mmgetstate", "-a"})
		if err != nil {
			return fmt.Errorf("mmgetstate -a (pod %s/%s): %w", spectrumScaleNS, podName, err)
		}
		if strings.TrimSpace(stderr) != "" {
			printInfo(fmt.Sprintf("mmgetstate stderr: %s", strings.TrimSpace(stderr)))
		}

		nonActive := parseMMGetStateNonActiveSummaries(stdout)
		if len(nonActive) == 0 {
			printSuccess("All GPFS nodes report active (mmgetstate -a)")
			return nil
		}
		lastNonActive = nonActive
		printInfo(fmt.Sprintf("GPFS nodes not all active: %s", strings.Join(nonActive, ", ")))

		down := parseMMGetStateDownNodes(stdout)
		if len(down) > 0 {
			printInfo(fmt.Sprintf("GPFS node(s) down: %s — deleting all worker daemon pods in %s", strings.Join(down, ", "), spectrumScaleNS))
			deleted, derr := deleteAllGpfsWorkerDaemonPods(mc, workers)
			if derr != nil {
				return derr
			}
			if deleted == 0 {
				printInfo("No Running GPFS worker daemon pods to delete")
			}
		} else {
			printInfo("No node in state down (non-active states may still be reported); no daemon pod delete this round")
		}

		printInfo(fmt.Sprintf("Waiting %v before next mmgetstate -a", gpfsClusterPostDaemonRestartWait))
		select {
		case <-mc.ctx.Done():
			return mc.ctx.Err()
		case <-time.After(gpfsClusterPostDaemonRestartWait):
		}
	}
}
