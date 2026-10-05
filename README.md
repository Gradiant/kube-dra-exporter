# kube-dra-exporter

Copyright 2026 Gradiant. Licensed under the [Apache License 2.0](LICENSE).

Maintainers:

- Carlos Giraldo — [@cgiraldo](https://github.com/cgiraldo), [cgiraldo@gradiant.org](mailto:cgiraldo@gradiant.org)
- Antía Rodal — [@arodalfer](https://github.com/arodalfer), [arodal@gradiant.org](mailto:arodal@gradiant.org)

This is a custom Prometheus exporter written in Go that collects real-time metrics about the status of `ResourceClaims` (`resource.k8s.io/v1`) in a Kubernetes cluster.

It is specifically designed for environments utilizing the native Dynamic Resource Allocation (DRA) API, such as Artificial Intelligence or HPC clusters that manage dynamic allocations of GPUs (NVIDIA/MIG) or other physical hardware accelerators.

## 🚀 Features

- **Efficient Design:** Utilizes a dynamic custom collector (`prometheus.Collector`) that queries the Kubernetes API on-demand during each scrape, preventing memory leaks or stale metrics from deleted resources.
- **Type-Safe Access:** Implements the standard Kubernetes typed client (`clientset`), ensuring optimal performance and full compatibility with the official Kubernetes API schema.
- **Dual Configuration Compatibility:** Automatically detects whether it is running locally (using your local `~/.kube/config` file) or inside a container within Kubernetes (using a `ServiceAccount`).

## 📊 Exposed Metrics

The exporter exposes the following metrics through the `/metrics` endpoint on port `8080`:


| Metric Name | Type | Description | Labels |
| :--- | :--- | :--- | :--- |
| `kube_resourceclaim_status_allocated_device` | Gauge | Shows `1.0` if a specific physical device has been successfully allocated. | `resourceclaim_name`, `namespace`, `pool`, `device_name`, `driver` |
| `kube_resourceclaim_status_reserved_for_pod` | Gauge | Shows `1.0` for each active Pod currently holding exclusive reservation of this claim. | `resourceclaim_name`, `namespace`, `pod_name`, `uid` |
| `kube_resourceclaim_status_reserved_total` | Gauge | Number of Pods reserving the ResourceClaim. | `resourceclaim_name`, `namespace` |
| `kube_resourceslice_device_capacity_memory_bytes` | Gauge | Device memory in bytes. | `resourceslice_name`, `device_name`, `driver`, `pool`, `node_name`, `type`, `product_name`, `parent_uuid` |
| `kube_resourceslice_device_capacity_multiprocessors` | Gauge | Device multiprocessor count. | `resourceslice_name`, `device_name`, `driver`, `pool`, `node_name`, `type`, `product_name`, `parent_uuid` |
| `kube_resourceslice_device_capacity_decoders` | Gauge | Device decoder count. | `resourceslice_name`, `device_name`, `driver`, `pool`, `node_name`, `type`, `product_name`, `parent_uuid` |
| `kube_deviceclass_hourly_cost` | Gauge | Hourly cost from the `hourly-cost` annotation. | `deviceclass_name` |

A failed query to any of the three APIs makes `/metrics` return HTTP 500, preventing a successful partial scrape. `/healthz` checks process liveness independently of API availability. The `hourly-cost` annotation accepts non-negative decimal numbers with a dot separator (for example, `2.50`); commas, exponents, NaN, and infinities are rejected. Invalid annotations are logged and omitted. Outside the cluster, `KUBECONFIG` supports multiple files separated by the OS path-list separator.

Capacity metrics include `driver`, `pool`, and `node_name`. Match allocations
using `(driver, pool, device_name)` within the same cluster and exporter target.
`node_name` comes from the ResourceSlice, or from the device when per-device node
selection is enabled; it is empty for shared or selector-based resources.
The exporter still reports all listed pool generations, and independently scraped
replicas produce separate series. Filter overlapping generations and deduplicate
replicas before joins or totals; these identity labels alone do not guarantee a
unique match across slices or targets.

## 📥 Quick Install

Install kube-dra-exporter with Helm:

```bash
helm upgrade --install kube-dra-exporter \
  oci://ghcr.io/gradiant/charts/kube-dra-exporter \
  --version 0.1.0 --namespace monitoring --create-namespace
```


## 🛠️ Prerequisites

- **Go:** Version 1.26 or higher (matching `go.mod` and the Docker build).
- **Kubernetes:** v1.34 or higher, serving `resourceclaims`, `resourceslices`, and `deviceclasses` under `resource.k8s.io/v1`. See the [DRA v1.34 release notes](https://v1-34.docs.kubernetes.io/blog/2025/09/01/kubernetes-v1-34-dra-updates/).
- **Core Go Modules:**
  - `github.com/prometheus/client_golang/prometheus`
  - `k8s.io/client-go`
  - `k8s.io/api`

## 💻 Local Development

1. Download the existing module dependencies:
   ```bash
   go mod download
   ```

2. Run the exporter directly in your terminal. It will automatically use your local `kubectl` configuration credentials:
   ```bash
   go run main.go
   ```

3. Verify it is running properly by opening your browser or using `curl`:
   ```bash
   curl http://localhost:8080/metrics
   ```

## ☸️ Kubernetes Deployment

To deploy this exporter safely inside your cluster, it requires cluster-wide `list` permissions for `resourceclaims`, `resourceslices`, and `deviceclasses`. ResourceClaims are collected across all namespaces; the other two resources are cluster-scoped.

Create a file named `deploy.yaml` with the following content to configure the **RBAC and Deployment**:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-dra-exporter-sa
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-dra-exporter-role
rules:
- apiGroups: ["resource.k8s.io"]
  resources: ["resourceclaims", "resourceslices", "deviceclasses"]
  verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-dra-exporter-binding
subjects:
- kind: ServiceAccount
  name: kube-dra-exporter-sa
  namespace: monitoring
roleRef:
  kind: ClusterRole
  name: kube-dra-exporter-role
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-dra-exporter
  namespace: monitoring
  labels:
    app: kube-dra-exporter
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kube-dra-exporter
  template:
    metadata:
      labels:
        app: kube-dra-exporter
    spec:
      serviceAccountName: kube-dra-exporter-sa
      containers:
      - name: exporter
        image: ghcr.io/gradiant/kube-dra-exporter:0.1.0
        ports:
        - containerPort: 8080
          name: http
        resources:
          limits:
            cpu: 100m
            memory: 128Mi
          requests:
            cpu: 50m
            memory: 64Mi
---
apiVersion: v1
kind: Service
metadata:
  name: kube-dra-exporter
  namespace: monitoring
  labels:
    app: kube-dra-exporter
spec:
  ports:
  - port: 8080
    targetPort: 8080
    name: http
  selector:
    app: kube-dra-exporter
```

Apply the manifest into your cluster:
```bash
kubectl apply -f deploy.yaml
```

## Helm configuration

After release `0.1.0` has been published, install the chart from GHCR:

```bash
helm upgrade --install kube-dra-exporter \
  oci://ghcr.io/gradiant/charts/kube-dra-exporter \
  --version 0.1.0 --namespace monitoring --create-namespace
```

Release images include `linux/amd64`, `linux/arm64`, `linux/ppc64le`,
`linux/s390x`, and `linux/riscv64` variants under the same tag.
The Dockerfile cross-compiles for the requested platform and uses a matching
distroless Debian 13 runtime image. To build all variants locally without publishing:

```bash
docker buildx build --platform linux/amd64,linux/arm64,linux/ppc64le,linux/s390x,linux/riscv64 \
  --output type=oci,dest=/tmp/kube-dra-exporter.tar .
```

The pod and container security contexts are configurable. Cluster administrators should enforce
admission rules with [Pod Security Admission](https://kubernetes.io/docs/concepts/security/pod-security-admission/)
or another supported admission policy mechanism.

The chart requires cluster-wide RBAC: `rbac.useClusterRole=false` is rejected.
Use `rbac.useExistingRole` to bind an existing ClusterRole, or `rbac.create=false`
when cluster-wide permissions are managed externally. Changing `namespace` moves
the exporter resources but does not limit collection to that namespace.

Set `replicaCount` to 2 or 3 for multiple independent collectors. There is no leader
election; avoid summing duplicate metrics across replicas. The default update
strategy is RollingUpdate. Set `podDisruptionBudget.enabled=true` to protect
against voluntary disruptions; it defaults to `minAvailable: 1`. Configure either
`minAvailable` or `maxUnavailable`, including zero or percentage values. A PDB
with one replica and `minAvailable: 1` can block node drains.

The exporter has no command-line flags. The chart rejects nonempty `extraArgs`,
nonempty `featureGates`, and the obsolete `maxConcurrentChallenges` option.
Configure credentials with its ServiceAccount or `KUBECONFIG` using `extraEnv`
and mounted files.

## Helm NetworkPolicy

Set `networkPolicy.enabled=true` in `deploy/helm/kube-dra-exporter` to create a
NetworkPolicy selecting this release's exporter pods. The cluster must use a CNI
plugin that enforces [Kubernetes NetworkPolicies](https://kubernetes.io/docs/concepts/services-networking/network-policies/);
creating the resource alone does not enforce isolation.

Default ingress permits only scraper pods labeled `app.kubernetes.io/name=prometheus`
in the exporter's namespace on TCP port `http` (8080). `prod-values.yaml` instead
permits those pods in the `monitoring` namespace. Adjust `networkPolicy.ingress`
to match your scraper's actual namespace and pod labels, including VictoriaMetrics
if used. ServiceMonitor labels and its namespace do not identify scraper pods.
Keep `namespaceSelector` and `podSelector` in the same `from` item so both must match.

Default egress permits TCP 443/6443 for the Kubernetes API and TCP/UDP 53 for DNS.
These ports are allowed to all destinations because API and DNS addresses vary
between clusters. Override `networkPolicy.egress` to restrict destinations or
support other API/DNS ports, accounting for NodeLocal DNS and your CNI's Service
address translation. All other ingress and egress is denied by this policy;
other policies selecting the same pods can grant additional access. Empty ingress
or egress lists deny that direction under this policy.

```bash
helm upgrade --install kube-dra-exporter deploy/helm/kube-dra-exporter \
  --namespace exporter --create-namespace \
  -f deploy/helm/kube-dra-exporter/prod-values.yaml \
  --set networkPolicy.enabled=true
```

After enabling, verify a scrape from the permitted Prometheus pods succeeds,
an unrelated pod cannot connect to port 8080, and the exporter can still query the
Kubernetes API. Helm rendering cannot verify your cluster's CNI enforcement.

## 🔍 Prometheus / VictoriaMetrics Integration

If you use the **Prometheus Operator**, you can add a `ServiceMonitor` resource to allow your monitoring system to automatically discover and scrape the generated metrics:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kube-dra-exporter-sm
  namespace: monitoring
  labels:
    release: prometheus # Adjust according to your Helm/Prometheus setup labels
spec:
  selector:
    matchLabels:
      app: kube-dra-exporter
  endpoints:
  - port: http
    interval: 15s
```

## Regression checks

Run `go test -race ./...`, `go vet ./...`, and `go build ./...` for the exporter.
For chart checks, install Helm and `PyYAML==6.0.2`, then run
`python -m unittest discover -s tests -v` and
`helm lint deploy/helm/kube-dra-exporter --strict`.
GitHub Actions runs these checks for pull requests and branch pushes; all image
and chart publishing workflows require the same validation to pass first.

## Releases

The public repository is `https://github.com/gradiant/kube-dra-exporter`.
The first release is `0.1.0`, with both `Chart.yaml` `version` and `appVersion`
set to `0.1.0`.

A single SemVer tag, such as `0.1.0` (or `v0.1.0`), triggers validation, then
publishes the multi-architecture image, then publishes the Helm chart. The tag
must match both `version` and `appVersion`; update both together for each release.
Leave the shipped `image.tag` empty to use
`appVersion`; shipped values profiles with a different tag or a pinned digest
fail release validation. Users may still override image tags or digests at install
time. App versions must be valid Docker tags, so SemVer build metadata (`+...`)
is not supported for app/image releases.

Releases publish to GitHub Packages (GHCR):

- Image: `ghcr.io/gradiant/kube-dra-exporter:0.1.0`
- Chart: `oci://ghcr.io/gradiant/charts/kube-dra-exporter`, version `0.1.0`

