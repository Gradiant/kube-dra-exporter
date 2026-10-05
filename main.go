package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ==========================================
// 1. COLLECTOR: ResourceClaimCollector
// ==========================================

type ResourceClaimCollector struct {
	clientset         kubernetes.Interface
	allocatedDesc     *prometheus.Desc
	reservedDesc      *prometheus.Desc
	reservedTotalDesc *prometheus.Desc
}

func NewResourceClaimCollector(clientset kubernetes.Interface) *ResourceClaimCollector {
	return &ResourceClaimCollector{
		clientset: clientset,
		allocatedDesc: prometheus.NewDesc(
			"kube_resourceclaim_status_allocated_device",
			"Reports 1 when a specific device is allocated to a ResourceClaim.",
			[]string{"resourceclaim_name", "namespace", "pool", "device_name", "driver"},
			nil,
		),
		reservedDesc: prometheus.NewDesc(
			"kube_resourceclaim_status_reserved_for_pod",
			"Reports 1 when the ResourceClaim is reserved for a specific Pod.",
			[]string{"resourceclaim_name", "namespace", "pod_name", "uid"},
			nil,
		),
		reservedTotalDesc: prometheus.NewDesc(
			"kube_resourceclaim_status_reserved_total",
			"Total number of Pods actively reserving this ResourceClaim.",
			[]string{"resourceclaim_name", "namespace"},
			nil,
		),
	}
}

func (c *ResourceClaimCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.allocatedDesc
	ch <- c.reservedDesc
	ch <- c.reservedTotalDesc
}

func (c *ResourceClaimCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	claims, err := c.clientset.ResourceV1().ResourceClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing ResourceClaims: %v", err)
		ch <- prometheus.NewInvalidMetric(c.allocatedDesc, fmt.Errorf("list ResourceClaims: %w", err))
		return
	}

	for _, claim := range claims.Items {
		name := claim.Name
		namespace := claim.Namespace

		if claim.Status.Allocation != nil {
			for _, dev := range claim.Status.Allocation.Devices.Results {
				ch <- prometheus.MustNewConstMetric(
					c.allocatedDesc,
					prometheus.GaugeValue,
					1.0,
					name, namespace, dev.Pool, dev.Device, dev.Driver,
				)
			}
		}

		podCount := 0
		for _, consumer := range claim.Status.ReservedFor {
			if consumer.Resource == "pods" && consumer.APIGroup == "" {
				podCount++
				ch <- prometheus.MustNewConstMetric(
					c.reservedDesc,
					prometheus.GaugeValue,
					1.0,
					name, namespace, consumer.Name, string(consumer.UID),
				)
			}
		}
		ch <- prometheus.MustNewConstMetric(
			c.reservedTotalDesc,
			prometheus.GaugeValue,
			float64(podCount),
			name, namespace,
		)
	}
}

// ==========================================
// 2. COLLECTOR: ResourceSliceCollector
// ==========================================

type ResourceSliceCollector struct {
	clientset             kubernetes.Interface
	sliceMemoryDesc       *prometheus.Desc
	sliceMultiprocessDesc *prometheus.Desc
	sliceDecodersDesc     *prometheus.Desc
}

func NewResourceSliceCollector(clientset kubernetes.Interface) *ResourceSliceCollector {
	return &ResourceSliceCollector{
		clientset: clientset,
		sliceMemoryDesc: prometheus.NewDesc(
			"kube_resourceslice_device_capacity_memory_bytes",
			"Device memory capacity in bytes.",
			[]string{"resourceslice_name", "device_name", "driver", "pool", "node_name", "type", "product_name", "parent_uuid"},
			nil,
		),
		sliceMultiprocessDesc: prometheus.NewDesc(
			"kube_resourceslice_device_capacity_multiprocessors",
			"Number of multiprocessors available on the device.",
			[]string{"resourceslice_name", "device_name", "driver", "pool", "node_name", "type", "product_name", "parent_uuid"},
			nil,
		),
		sliceDecodersDesc: prometheus.NewDesc(
			"kube_resourceslice_device_capacity_decoders",
			"Number of hardware decoders available on the device.",
			[]string{"resourceslice_name", "device_name", "driver", "pool", "node_name", "type", "product_name", "parent_uuid"},
			nil,
		),
	}
}

func (c *ResourceSliceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.sliceMemoryDesc
	ch <- c.sliceMultiprocessDesc
	ch <- c.sliceDecodersDesc
}

func (c *ResourceSliceCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	slices, err := c.clientset.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing ResourceSlices: %v", err)
		ch <- prometheus.NewInvalidMetric(c.sliceMemoryDesc, fmt.Errorf("list ResourceSlices: %w", err))
		return
	}

	for _, slice := range slices.Items {
		sliceName := slice.Name

		for _, device := range slice.Spec.Devices {
			deviceName := device.Name
			// A shared or selector-based resource has no single explicit node.
			nodeName := ""
			if slice.Spec.PerDeviceNodeSelection != nil && *slice.Spec.PerDeviceNodeSelection {
				if device.NodeName != nil {
					nodeName = *device.NodeName
				}
			} else if slice.Spec.NodeName != nil {
				nodeName = *slice.Spec.NodeName
			}
			var deviceType, productName, parentUUID string

			// Safely read DeviceAttribute string values.
			if attr, ok := device.Attributes["type"]; ok && attr.StringValue != nil {
				deviceType = *attr.StringValue
			}
			if attr, ok := device.Attributes["productName"]; ok && attr.StringValue != nil {
				productName = *attr.StringValue
			}
			if attr, ok := device.Attributes["parentUUID"]; ok && attr.StringValue != nil {
				parentUUID = *attr.StringValue
			}

			// Convert the memory quantity to bytes using AsApproximateFloat64().
			if mem, ok := device.Capacity["memory"]; ok {
				val := mem.Value.AsApproximateFloat64()
				ch <- prometheus.MustNewConstMetric(
					c.sliceMemoryDesc,
					prometheus.GaugeValue,
					val,
					sliceName, deviceName, slice.Spec.Driver, slice.Spec.Pool.Name, nodeName, deviceType, productName, parentUUID,
				)
			}

			// Process multiprocessor capacity.
			if mp, ok := device.Capacity["multiprocessors"]; ok {
				val := mp.Value.AsApproximateFloat64()
				ch <- prometheus.MustNewConstMetric(
					c.sliceMultiprocessDesc,
					prometheus.GaugeValue,
					val,
					sliceName, deviceName, slice.Spec.Driver, slice.Spec.Pool.Name, nodeName, deviceType, productName, parentUUID,
				)
			}

			// Process decoder capacity.
			if dec, ok := device.Capacity["decoders"]; ok {
				val := dec.Value.AsApproximateFloat64()
				ch <- prometheus.MustNewConstMetric(
					c.sliceDecodersDesc,
					prometheus.GaugeValue,
					val,
					sliceName, deviceName, slice.Spec.Driver, slice.Spec.Pool.Name, nodeName, deviceType, productName, parentUUID,
				)
			}
		}
	}
}

// ==========================================
// 3. COLLECTOR: DeviceClassCollector
// ==========================================

type DeviceClassCollector struct {
	clientset kubernetes.Interface
	costDesc  *prometheus.Desc
}

func NewDeviceClassCollector(clientset kubernetes.Interface) *DeviceClassCollector {
	return &DeviceClassCollector{
		clientset: clientset,
		costDesc: prometheus.NewDesc(
			"kube_deviceclass_hourly_cost",
			"Hourly cost of the DeviceClass resource from its annotations.",
			[]string{"deviceclass_name"},
			nil,
		),
	}
}

func (c *DeviceClassCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.costDesc
}

func (c *DeviceClassCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// DeviceClass is a cluster-scoped resource (it has no namespace).
	deviceClasses, err := c.clientset.ResourceV1().DeviceClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing DeviceClasses: %v", err)
		ch <- prometheus.NewInvalidMetric(c.costDesc, fmt.Errorf("list DeviceClasses: %w", err))
		return
	}

	for _, dc := range deviceClasses.Items {
		// Check whether the annotation exists.
		if costStr, ok := dc.Annotations["hourly-cost"]; ok {
			costVal, err := parseHourlyCost(costStr)
			if err != nil {
				log.Printf("Error parsing 'hourly-cost' (%s) on DeviceClass %s: %v", costStr, dc.Name, err)
				continue
			}

			ch <- prometheus.MustNewConstMetric(
				c.costDesc,
				prometheus.GaugeValue,
				costVal,
				dc.Name,
			)
		}
	}
}

// ==========================================
// CONFIGURATION AND HTTP SERVER
// ==========================================

// Hourly costs use non-negative decimal notation with a dot as the separator.
// Grouping separators, exponents, and non-finite values are deliberately rejected.
var hourlyCostPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func parseHourlyCost(raw string) (float64, error) {
	value := strings.TrimSpace(raw)
	if !hourlyCostPattern.MatchString(value) {
		return 0, fmt.Errorf("expected a non-negative decimal with a dot separator")
	}
	cost, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, fmt.Errorf("hourly cost is outside the finite float64 range")
	}
	return cost, nil
}

func loadKubeConfig() (*rest.Config, error) {
	var config *rest.Config
	var err error

	config, err = rest.InClusterConfig()
	if err != nil {
		log.Println("No in-cluster Kubernetes configuration detected. Loading local kubeconfig...")
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{},
		).ClientConfig()
	}
	return config, err
}

func metricsHandler(clientset kubernetes.Interface) http.Handler {
	// Register the claim, slice, and device class collectors in an isolated registry.
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewResourceClaimCollector(clientset))
	registry.MustRegister(NewResourceSliceCollector(clientset))
	registry.MustRegister(NewDeviceClassCollector(clientset))
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{ErrorHandling: promhttp.HTTPErrorOnError})
}

func exporterHandler(clientset kubernetes.Interface) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsHandler(clientset))
	// Process liveness is independent of Kubernetes API availability.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func main() {
	config, err := loadKubeConfig()
	if err != nil {
		log.Fatalf("Failed to load Kubernetes configuration: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to initialize the typed Kubernetes client (Clientset): %v", err)
	}

	log.Println("Modular exporter running at http://localhost:8080/metrics")
	if err := http.ListenAndServe(":8080", exporterHandler(clientset)); err != nil {
		log.Fatalf("Failed to start the HTTP server: %v", err)
	}
}
