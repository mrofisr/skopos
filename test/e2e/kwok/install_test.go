//go:build e2e

// Package kwok_test installs and upgrades the Helm chart against a KWOK
// cluster. KWOK fakes the pod lifecycle, so this proves the API server accepts
// the rendered chart, workloads schedule onto a fake node, PVCs bind to static
// volumes, Helm hooks complete, and Services get endpoints. It does not
// execute containers, probes, ClickHouse SQL, or OBI eBPF.
package kwok_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	namespace = "skopos-kwok"
	release   = "skopos"
	chart     = "../../../deploy/helm/skopos"
	nodeName  = "kwok-node-0"
)

func TestHelmInstallAndUpgrade(t *testing.T) {
	requireTools(t)
	applyFixtures(t)
	t.Cleanup(func() { cleanup(t) })

	installDataPlane(t)
	assertReleaseDeployed(t)
	assertDataPlane(t)

	upgradeGateway(t)
	assertReleaseDeployed(t)
	assertGateway(t)
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"helm", "kubectl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required for the KWOK install test: %v", tool, err)
		}
	}
}

func applyFixtures(t *testing.T) {
	t.Helper()
	run(t, "kubectl", "apply", "-f", "testdata/node.yaml")
	run(t, "kubectl", "apply", "-f", "testdata/storage.yaml")
	run(t, "kubectl", "wait", "--for=condition=Ready", "node/"+nodeName, "--timeout=2m")
}

func installDataPlane(t *testing.T) {
	t.Helper()
	run(t, "helm", "install", release, chart,
		"--namespace", namespace,
		"--create-namespace",
		"--set", "clickhouse.persistence.storageClass=kwok-clickhouse",
		"--set", "collector.sendingQueue.persistence.storageClass=kwok-collector",
		"--wait",
		"--timeout", "10m",
	)
}

func upgradeGateway(t *testing.T) {
	t.Helper()
	run(t, "helm", "upgrade", release, chart,
		"--namespace", namespace,
		"--set", "clickhouse.persistence.storageClass=kwok-clickhouse",
		"--set", "collector.sendingQueue.persistence.storageClass=kwok-collector",
		"--set", "gateway.enabled=true",
		"--set", "gateway.image.repository=example.invalid/skopos-gateway",
		"--set", "gateway.image.tag=ci",
		"--set", "gateway.image.digest=",
		"--set", "gateway.image.pullPolicy=Never",
		"--wait",
		"--timeout", "5m",
	)
}

func assertReleaseDeployed(t *testing.T) {
	t.Helper()
	out := runOutput(t, "helm", "status", release, "--namespace", namespace, "--output", "json")
	var status struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatalf("decode helm status: %v\n%s", err, out)
	}
	if status.Info.Status != "deployed" {
		t.Fatalf("helm release status = %q, want deployed", status.Info.Status)
	}
}

func assertDataPlane(t *testing.T) {
	t.Helper()
	for _, workload := range []string{
		"statefulset/skopos-clickhouse",
		"statefulset/skopos-collector",
		"daemonset/skopos-obi",
	} {
		run(t, "kubectl", "rollout", "status", workload,
			"--namespace", namespace,
			"--timeout", "5m",
		)
	}
	run(t, "kubectl", "wait", "--for=condition=Complete",
		"job/skopos-clickhouse-init",
		"--namespace", namespace,
		"--timeout", "5m",
	)
	assertPods(t, "clickhouse", "collector", "obi", "clickhouse-init")
}

func assertGateway(t *testing.T) {
	t.Helper()
	run(t, "kubectl", "rollout", "status", "deployment/skopos-gateway",
		"--namespace", namespace,
		"--timeout", "5m",
	)
	assertPods(t, "gateway")
	assertEndpoints(t, "skopos-gateway")
}

func assertPods(t *testing.T, components ...string) {
	t.Helper()
	out := runOutput(t, "kubectl", "get", "pods", "--namespace", namespace, "--output", "json")
	var list struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
				PodIP string `json:"podIP"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatalf("decode pod list: %v\n%s", err, out)
	}

	want := make(map[string]bool, len(components))
	for _, component := range components {
		want[component] = false
	}
	for _, pod := range list.Items {
		component := pod.Metadata.Labels["app.kubernetes.io/component"]
		if _, ok := want[component]; !ok {
			continue
		}
		if pod.Spec.NodeName != nodeName {
			t.Errorf("%s pod is on node %q, want %q", component, pod.Spec.NodeName, nodeName)
		}
		if pod.Status.Phase != "Running" && pod.Status.Phase != "Succeeded" {
			t.Errorf("%s pod phase = %q, want Running or Succeeded", component, pod.Status.Phase)
		}
		if pod.Status.PodIP == "" {
			t.Errorf("%s pod has no podIP", component)
		}
		want[component] = true
	}
	for component, seen := range want {
		if !seen {
			t.Errorf("no %s pod was found in namespace %s", component, namespace)
		}
	}
}

func assertEndpoints(t *testing.T, service string) {
	t.Helper()
	out := runOutput(t, "kubectl", "get", "endpoints", service, "--namespace", namespace, "--output", "json")
	var endpoints struct {
		Subsets []struct {
			Addresses []struct {
				IP string `json:"ip"`
			} `json:"addresses"`
		} `json:"subsets"`
	}
	if err := json.Unmarshal(out, &endpoints); err != nil {
		t.Fatalf("decode endpoints %s: %v\n%s", service, err, out)
	}
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			return
		}
	}
	t.Fatalf("service %s has no ready endpoint addresses", service)
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// runOutput separates stdout from stderr because kubectl writes deprecation
// warnings to stderr. The JSON commands need stdout alone.
func runOutput(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s\n%s", name, strings.Join(args, " "), err, stderr.String(), out)
	}
	return out
}

func cleanup(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("helm", "uninstall", release,
		"--namespace", namespace, "--wait").CombinedOutput(); err != nil {
		t.Logf("helm uninstall: %v\n%s", err, out)
	}
	if out, err := exec.Command("kubectl", "delete", "namespace", namespace,
		"--ignore-not-found", "--wait=false").CombinedOutput(); err != nil {
		t.Logf("delete namespace: %v\n%s", err, out)
	}
	if out, err := exec.Command("kubectl", "delete", "pv",
		"skopos-clickhouse-data", "skopos-collector-queue",
		"--ignore-not-found", "--wait=false").CombinedOutput(); err != nil {
		t.Logf("delete persistent volumes: %v\n%s", err, out)
	}
}
