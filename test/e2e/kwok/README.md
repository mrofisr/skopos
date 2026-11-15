# KWOK installation harness

This harness installs and upgrades the local Helm chart in a
[KWOK](https://kwok.sigs.k8s.io/) cluster. It is a black-box test: it uses
`helm` and `kubectl` only.

## What it proves

- The API server accepts the rendered chart.
- ClickHouse, the Collector, OBI, and the gateway schedule onto a fake node.
- The static volumes bind the ClickHouse and Collector queue claims.
- The ClickHouse init Job hook completes.
- StatefulSets, the DaemonSet, Deployments, and Services report ready.
- The gateway Service gets endpoint addresses.

## What it does not prove

KWOK fakes the pod lifecycle. This harness does not start containers, run
probes, execute ClickHouse SQL, or exercise OBI eBPF. Those paths remain covered
by the ClickHouse integration, compatibility, and OBI harnesses.

## Run locally

Requires `kwokctl` v0.8.0, `helm`, `kubectl`, and Go. The harness applies
`testdata/node.yaml` and `testdata/storage.yaml` itself.

```sh
export KUBECONFIG="$(mktemp)"
kwokctl create cluster --name skopos-test --runtime binary --kubeconfig "$KUBECONFIG"

go test -tags=e2e ./test/e2e/kwok -count=1 -v -timeout 20m

kwokctl delete cluster --name skopos-test
```

The CI job uses Kubernetes v1.34.8. To run the same version locally:

```sh
export KWOK_KUBE_VERSION=v1.34.8
```

The test creates the `skopos-kwok` namespace, the `skopos-clickhouse-data` and
`skopos-collector-queue` persistent volumes, and the `kwok-clickhouse` and
`kwok-collector` storage classes. Cleanup uninstalls the Helm release and
deletes those objects.
