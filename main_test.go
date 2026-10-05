package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestCollectionFailureFailsScrape(t *testing.T) {
	for _, name := range []string{"resourceclaims", "resourceslices", "deviceclasses"} {
		for _, failure := range []struct {
			name string
			err  error
		}{
			{"forbidden", apierrors.NewForbidden(schema.GroupResource{Group: "resource.k8s.io", Resource: name}, "", errors.New("denied"))},
			{"unavailable", apierrors.NewServiceUnavailable("API unavailable")},
		} {
			t.Run(name+"/"+failure.name, func(t *testing.T) {
				client := fake.NewClientset(&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "gpu", Annotations: map[string]string{"hourly-cost": "2.5"}}})
				client.PrependReactor("list", name, func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, failure.err })
				handler := exporterHandler(client)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
				if response.Code != http.StatusInternalServerError {
					t.Fatalf("scrape status = %d, want 500: %s", response.Code, response.Body.String())
				}
				if strings.Contains(response.Body.String(), "kube_deviceclass_hourly_cost{") {
					t.Fatal("failure returned partial successful metrics")
				}
				health := httptest.NewRecorder()
				handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
				if health.Code != http.StatusOK {
					t.Fatalf("health status = %d", health.Code)
				}
			})
		}
	}
}

func TestSuccessfulScrape(t *testing.T) {
	str := func(s string) *string { return &s }
	client := fake.NewClientset(
		&resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "workloads"}, Status: resourcev1.ResourceClaimStatus{
			Allocation:  &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "gpu.example", Pool: "pool", Device: "gpu0"}}}},
			ReservedFor: []resourcev1.ResourceClaimConsumerReference{{Resource: "pods", Name: "worker", UID: "uid"}, {Resource: "jobs", Name: "ignored"}},
		}},
		&resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: "slice"}, Spec: resourcev1.ResourceSliceSpec{Driver: "gpu.example", Pool: resourcev1.ResourcePool{Name: "pool", Generation: 1, ResourceSliceCount: 1}, NodeName: str("worker-node"), Devices: []resourcev1.Device{{Name: "gpu0", Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{"type": {StringValue: str("gpu")}}, Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{"memory": {Value: resource.MustParse("1Gi")}, "multiprocessors": {Value: resource.MustParse("16")}, "decoders": {Value: resource.MustParse("2")}}}}}},
		&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "gpu", Annotations: map[string]string{"hourly-cost": "2.50"}}},
	)
	response := httptest.NewRecorder()
	exporterHandler(client).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	for _, line := range []string{
		`kube_resourceclaim_status_allocated_device{device_name="gpu0",driver="gpu.example",namespace="workloads",pool="pool",resourceclaim_name="claim"} 1`,
		`kube_resourceclaim_status_reserved_for_pod{namespace="workloads",pod_name="worker",resourceclaim_name="claim",uid="uid"} 1`,
		`kube_resourceclaim_status_reserved_total{namespace="workloads",resourceclaim_name="claim"} 1`,
		`kube_resourceslice_device_capacity_memory_bytes{device_name="gpu0",driver="gpu.example",node_name="worker-node",parent_uuid="",pool="pool",product_name="",resourceslice_name="slice",type="gpu"} 1.073741824e+09`,
		`kube_resourceslice_device_capacity_multiprocessors{device_name="gpu0",driver="gpu.example",node_name="worker-node",parent_uuid="",pool="pool",product_name="",resourceslice_name="slice",type="gpu"} 16`,
		`kube_resourceslice_device_capacity_decoders{device_name="gpu0",driver="gpu.example",node_name="worker-node",parent_uuid="",pool="pool",product_name="",resourceslice_name="slice",type="gpu"} 2`,
		`kube_deviceclass_hourly_cost{deviceclass_name="gpu"} 2.5`,
	} {
		if !strings.Contains(response.Body.String(), line) {
			t.Errorf("missing metric %s in %s", line, response.Body.String())
		}
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "list" || action.GetNamespace() != "" {
			t.Errorf("unexpected API action: %#v", action)
		}
	}
}

func TestEmptySuccessfulScrape(t *testing.T) {
	response := httptest.NewRecorder()
	metricsHandler(fake.NewClientset()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("empty scrape status = %d", response.Code)
	}
}

func TestParseHourlyCost(t *testing.T) {
	for input, want := range map[string]float64{"0": 0, "0.00": 0, " 2.50 ": 2.5, "001.25": 1.25} {
		got, err := parseHourlyCost(input)
		if err != nil || got != want {
			t.Errorf("parse %q = %v, %v; want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"", "NaN", "Inf", "+Inf", "-Inf", "-1", "1,234", "1,23", "1.234,56", "1,234.56", "1e3", "0x1p2", "1_000", ".5", "1.", "+2", strings.Repeat("9", 400)} {
		if _, err := parseHourlyCost(input); err == nil {
			t.Errorf("accepted invalid cost %q", input)
		}
	}
}

func TestInvalidHourlyCostsAreOmitted(t *testing.T) {
	client := fake.NewClientset(
		&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "invalid", Annotations: map[string]string{"hourly-cost": "NaN"}}},
		&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "ambiguous", Annotations: map[string]string{"hourly-cost": "1,234"}}},
		&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "missing"}},
		&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "valid", Annotations: map[string]string{"hourly-cost": "3.25"}}},
	)
	response := httptest.NewRecorder()
	metricsHandler(client).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `kube_deviceclass_hourly_cost{deviceclass_name="valid"} 3.25`) {
		t.Fatal(response.Body.String())
	}
	for _, name := range []string{"invalid", "ambiguous", "missing"} {
		if strings.Contains(response.Body.String(), `deviceclass_name="`+name+`"`) {
			t.Errorf("emitted cost for %s", name)
		}
	}
}

func TestMergedKubeconfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	dir := t.TempDir()
	clusterPath, userPath := filepath.Join(dir, "cluster.yaml"), filepath.Join(dir, "user.yaml")
	cluster := `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://api.example:6443
contexts:
- name: test
  context:
    cluster: test
    user: scraper
current-context: test
`
	user := `apiVersion: v1
kind: Config
users:
- name: scraper
  user:
    token: test-token
`
	for path, content := range map[string]string{clusterPath: cluster, userPath: user} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KUBECONFIG", strings.Join([]string{clusterPath, userPath}, string(os.PathListSeparator)))
	config, err := loadKubeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "https://api.example:6443" || config.BearerToken != "test-token" {
		t.Fatalf("merged configuration = %#v", config)
	}
}

func TestResourceSliceCapacityNodeIdentity(t *testing.T) {
	node := "worker-node"
	perDevice, allNodes := true, true
	selector := &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "region", Operator: corev1.NodeSelectorOpIn, Values: []string{"west"}}}}}}
	for _, tc := range []struct {
		name     string
		spec     resourcev1.ResourceSliceSpec
		device   resourcev1.Device
		wantNode string
	}{
		{name: "slice-node", spec: resourcev1.ResourceSliceSpec{NodeName: &node}, wantNode: node},
		{name: "slice-shared", spec: resourcev1.ResourceSliceSpec{AllNodes: &allNodes}},
		{name: "slice-selector", spec: resourcev1.ResourceSliceSpec{NodeSelector: selector}},
		{name: "per-device-node", spec: resourcev1.ResourceSliceSpec{PerDeviceNodeSelection: &perDevice}, device: resourcev1.Device{NodeName: &node}, wantNode: node},
		{name: "per-device-shared", spec: resourcev1.ResourceSliceSpec{PerDeviceNodeSelection: &perDevice}, device: resourcev1.Device{AllNodes: &allNodes}},
		{name: "per-device-selector", spec: resourcev1.ResourceSliceSpec{PerDeviceNodeSelection: &perDevice}, device: resourcev1.Device{NodeSelector: selector}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Driver = "gpu.example"
			// The pool name must not be treated as a node identity.
			tc.spec.Pool = resourcev1.ResourcePool{Name: "pool-is-not-a-node", Generation: 1, ResourceSliceCount: 1}
			tc.device.Name = "gpu0"
			tc.device.Capacity = map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
				"memory":          {Value: resource.MustParse("1Gi")},
				"multiprocessors": {Value: resource.MustParse("16")},
				"decoders":        {Value: resource.MustParse("2")},
			}
			tc.spec.Devices = []resourcev1.Device{tc.device}
			client := fake.NewClientset(&resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: "slice"}, Spec: tc.spec})
			registry := prometheus.NewPedanticRegistry()
			registry.MustRegister(NewResourceSliceCollector(client))
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			if len(families) != 3 {
				t.Fatalf("got %d capacity families, want 3", len(families))
			}
			want := map[string]string{"resourceslice_name": "slice", "device_name": "gpu0", "driver": "gpu.example", "pool": "pool-is-not-a-node", "node_name": tc.wantNode, "type": "", "product_name": "", "parent_uuid": ""}
			for _, family := range families {
				if len(family.Metric) != 1 {
					t.Fatalf("%s emitted %d samples, want 1", family.GetName(), len(family.Metric))
				}
				metric := family.Metric[0]
				if len(metric.Label) != len(want) {
					t.Fatalf("%s has %d labels, want %d", family.GetName(), len(metric.Label), len(want))
				}
				for _, label := range metric.Label {
					value, ok := want[label.GetName()]
					if !ok || label.GetValue() != value {
						t.Errorf("%s label %s=%q, want %q", family.GetName(), label.GetName(), label.GetValue(), value)
					}
				}
			}
		})
	}
}

func TestResourceSliceCapacityDriverAndPoolIdentity(t *testing.T) {
	type identity struct{ driver, pool, device string }
	identities := []identity{{"driver-a.example", "pool-a", "gpu0"}, {"driver-b.example", "pool-a", "gpu0"}, {"driver-a.example", "pool-b", "gpu0"}}
	client := fake.NewClientset()
	allNodes := true
	for i, key := range identities {
		slice := &resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: []string{"slice-a", "slice-b", "slice-c"}[i]}, Spec: resourcev1.ResourceSliceSpec{
			Driver: key.driver, Pool: resourcev1.ResourcePool{Name: key.pool, Generation: 1, ResourceSliceCount: 1}, AllNodes: &allNodes,
			Devices: []resourcev1.Device{{Name: key.device, Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{"memory": {Value: resource.MustParse("1Gi")}}}},
		}}
		if err := client.Tracker().Add(slice); err != nil {
			t.Fatal(err)
		}
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(NewResourceSliceCollector(client))
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 || len(families[0].Metric) != 3 {
		t.Fatalf("expected three distinct capacity samples, got %v", families)
	}
	found := map[identity]bool{}
	for _, metric := range families[0].Metric {
		labels := map[string]string{}
		for _, label := range metric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		found[identity{labels["driver"], labels["pool"], labels["device_name"]}] = true
	}
	for _, key := range identities {
		if !found[key] {
			t.Errorf("missing device identity %+v", key)
		}
	}
}
