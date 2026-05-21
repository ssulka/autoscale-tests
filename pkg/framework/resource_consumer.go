package framework

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	ResourceConsumerImage = "registry.k8s.io/e2e-test-images/resource-consumer:1.13"

	resourceConsumerPort       = 8080
	resourceConsumerServicePort = 80

	consumptionDurationSec = 30
)

// SidecarMode controls whether a second resource-consumer container is added to the pod.
type SidecarMode int

const (
	SidecarDisabled SidecarMode = iota // no sidecar
	SidecarIdle                        // sidecar present but no load generated on it
	SidecarBusy                        // sidecar present and actively consuming CPU
)

// ResourceConsumerConfig holds all parameters for deploying a resource-consumer workload.
type ResourceConsumerConfig struct {
	Name          string
	Namespace     string
	Replicas      int32
	CPURequest    int64 // millicores
	MemRequest    int64 // megabytes 
	MemLimit      int64 // megabytes 
	InitCPUTotal  int   // millicores — initial CPU consumption across all pods
	InitMemTotal  int   // megabytes — initial memory consumption across all pods
	Sidecar       SidecarMode
}

type ResourceConsumer struct {
	name      string
	namespace string
	f         *Framework

	cpu     chan int
	mem     chan int
	stopCPU chan struct{}
	stopMem chan struct{}
	wg      sync.WaitGroup
}

// CreateResourceConsumer deploys a resource-consumer Deployment + Service,
// waits for pods to be ready, starts background load loops, and optionally
// applies initial CPU/memory load.
func (f *Framework) CreateResourceConsumer(ctx context.Context, cfg ResourceConsumerConfig) (*ResourceConsumer, error) {
	labels := map[string]string{"name": cfg.Name}

	// Memory limit must be higher than request to allow burst without OOMKill.
	// HPA computes utilization based on request, not limit.
	memLimit := cfg.MemLimit
	if memLimit == 0 {
		memLimit = cfg.MemRequest * 2
		if memLimit < 1024 {
			memLimit = 1024
		}
	}

	containers := []corev1.Container{{
		Name:    cfg.Name,
		Image:   ResourceConsumerImage,
		Ports:   []corev1.ContainerPort{{ContainerPort: resourceConsumerPort}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cfg.CPURequest, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(cfg.MemRequest*1024*1024, resource.BinarySI),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cfg.CPURequest, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(memLimit*1024*1024, resource.BinarySI),
			},
		},
	}}

	// Optional sidecar — same image on port 8081, used for ContainerResource HPA tests.
	if cfg.Sidecar != SidecarDisabled {
		sidecar := corev1.Container{
			Name:    cfg.Name + "-sidecar",
			Image:   ResourceConsumerImage,
			Command: []string{"/consumer", "-port=8081"},
			Ports:   []corev1.ContainerPort{{ContainerPort: 8081}},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(cfg.CPURequest, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(cfg.MemRequest*1024*1024, resource.BinarySI),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(cfg.CPURequest, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(memLimit*1024*1024, resource.BinarySI),
				},
			},
		}
		containers = append(containers, sidecar)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &cfg.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: containers},
			},
		},
	}
	if err := f.Client.Create(ctx, deployment); err != nil {
		return nil, fmt.Errorf("failed to create resource-consumer deployment: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Port:       resourceConsumerServicePort,
				TargetPort: intstr.FromInt32(resourceConsumerPort),
			}},
			Selector: labels,
		},
	}
	if err := f.Client.Create(ctx, svc); err != nil {
		return nil, fmt.Errorf("failed to create resource-consumer service: %w", err)
	}

	if err := f.WaitForDeploymentReady(ctx, cfg.Name, cfg.Namespace, 3*time.Minute); err != nil {
		return nil, fmt.Errorf("resource-consumer deployment not ready: %w", err)
	}

	if err := f.waitForServiceEndpoints(ctx, cfg.Name, cfg.Namespace, 2*time.Minute); err != nil {
		return nil, fmt.Errorf("resource-consumer service has no endpoints: %w", err)
	}

	rc := &ResourceConsumer{
		name:      cfg.Name,
		namespace: cfg.Namespace,
		f:         f,
		cpu:       make(chan int),
		mem:       make(chan int),
		stopCPU:   make(chan struct{}),
		stopMem:   make(chan struct{}),
	}

	go rc.consumeCPULoop(ctx)
	go rc.consumeMemLoop(ctx)

	if cfg.InitCPUTotal > 0 {
		rc.ConsumeCPU(cfg.InitCPUTotal)
	}
	if cfg.InitMemTotal > 0 {
		rc.ConsumeMem(cfg.InitMemTotal)
	}

	// For SidecarBusy, saturate the sidecar container to its full CPU request.
	// This is used in "do not scale on busy sidecar" tests where HPA watches
	// only the main container — the sidecar should not influence scaling.
	if cfg.Sidecar == SidecarBusy {
		rc.sendSidecarConsumeCPU(ctx, int(cfg.CPURequest))
	}

	return rc, nil
}

func (rc *ResourceConsumer) ConsumeCPU(millicores int) {
	rc.cpu <- millicores
}

// ConsumeMem sets a new total memory load (in megabytes) distributed across all pods.
func (rc *ResourceConsumer) ConsumeMem(megabytes int) {
	rc.mem <- megabytes
}

// CleanUp stops the background load loops and waits for them to finish.
func (rc *ResourceConsumer) CleanUp() {
	close(rc.stopCPU)
	close(rc.stopMem)
	rc.wg.Wait()
}

func (rc *ResourceConsumer) consumeCPULoop(ctx context.Context) {
	rc.wg.Add(1)
	defer rc.wg.Done()

	millicores := 0
	for {
		select {
		case millicores = <-rc.cpu:
			rc.sendConsumeCPU(ctx, millicores)
		case <-time.After(time.Duration(consumptionDurationSec) * time.Second):
			if millicores > 0 {
				rc.sendConsumeCPU(ctx, millicores)
			}
		case <-ctx.Done():
			return
		case <-rc.stopCPU:
			return
		}
	}
}

// consumeMemLoop is the memory equivalent of consumeCPULoop.
func (rc *ResourceConsumer) consumeMemLoop(ctx context.Context) {
	rc.wg.Add(1)
	defer rc.wg.Done()

	megabytes := 0
	for {
		select {
		case megabytes = <-rc.mem:
			rc.sendConsumeMem(ctx, megabytes)
		case <-time.After(time.Duration(consumptionDurationSec) * time.Second):
			if megabytes > 0 {
				rc.sendConsumeMem(ctx, megabytes)
			}
		case <-ctx.Done():
			return
		case <-rc.stopMem:
			return
		}
	}
}

func (rc *ResourceConsumer) sendConsumeCPU(ctx context.Context, millicores int) {
	rc.sendPerPod(ctx, resourceConsumerPort, "ConsumeCPU", "millicores", millicores)
}

func (rc *ResourceConsumer) sendConsumeMem(ctx context.Context, megabytes int) {
	rc.sendPerPod(ctx, resourceConsumerPort, "ConsumeMem", "megabytes", megabytes)
}

// sendPerPod distributes a total resource amount evenly across all running pods
// by calling the resource-consumer HTTP API on each pod directly via the pod proxy.
func (rc *ResourceConsumer) sendPerPod(ctx context.Context, port int32, endpoint, paramName string, totalAmount int) {
	pods, err := rc.f.Clientset.CoreV1().Pods(rc.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "name=" + rc.name,
	})
	if err != nil || len(pods.Items) == 0 {
		fmt.Printf("[ResourceConsumer] sendPerPod: failed to list pods: %v\n", err)
		rc.sendRequest(ctx, rc.name, resourceConsumerServicePort, endpoint,
			map[string]string{
				paramName:     strconv.Itoa(totalAmount),
				"durationSec": strconv.Itoa(consumptionDurationSec + 10),
			})
		return
	}

	runningPods := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			runningPods++
		}
	}
	if runningPods == 0 {
		fmt.Printf("[ResourceConsumer] sendPerPod: no running pods, falling back to service proxy\n")
		rc.sendRequest(ctx, rc.name, resourceConsumerServicePort, endpoint,
			map[string]string{
				paramName:     strconv.Itoa(totalAmount),
				"durationSec": strconv.Itoa(consumptionDurationSec + 10),
			})
		return
	}

	perPod := totalAmount / runningPods
	remainder := totalAmount % runningPods
	sent := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		amount := perPod
		if sent == 0 {
			amount += remainder
		}
		sent++
		req := rc.f.Clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Namespace(rc.namespace).
			Name(fmt.Sprintf("%s:%d", pod.Name, port)).
			SubResource("proxy").
			Suffix(endpoint).
			Param(paramName, strconv.Itoa(amount)).
			Param("durationSec", strconv.Itoa(consumptionDurationSec+10))
		_, reqErr := req.DoRaw(ctx)
		if reqErr != nil {
			fmt.Printf("[ResourceConsumer] sendPerPod %s to %s failed: %v\n", endpoint, pod.Name, reqErr)
		}
	}
}

// sendSidecarConsumeCPU sends CPU load to the sidecar container (port 8081) on each pod.
// Used only in SidecarBusy mode to simulate a busy sidecar that HPA should ignore
// when configured with ContainerResource metrics targeting the main container.
func (rc *ResourceConsumer) sendSidecarConsumeCPU(ctx context.Context, millicores int) {
	pods, err := rc.f.Clientset.CoreV1().Pods(rc.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "name=" + rc.name,
	})
	if err != nil || len(pods.Items) == 0 {
		fmt.Printf("[ResourceConsumer] failed to list pods for sidecar CPU request: %v\n", err)
		return
	}
	for _, pod := range pods.Items {
		req := rc.f.Clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Namespace(rc.namespace).
			Name(fmt.Sprintf("%s:%d", pod.Name, 8081)).
			SubResource("proxy").
			Suffix("ConsumeCPU").
			Param("millicores", strconv.Itoa(millicores)).
			Param("durationSec", strconv.Itoa(consumptionDurationSec+10))
		if _, err := req.DoRaw(ctx); err != nil {
			fmt.Printf("[ResourceConsumer] sidecar ConsumeCPU failed for pod %s: %v\n", pod.Name, err)
		}
	}
}

// sendRequest sends an HTTP POST to a service via the Kubernetes service proxy API.
// Retries up to 3 times with 5s backoff. Used as a fallback when per-pod delivery fails.
func (rc *ResourceConsumer) sendRequest(ctx context.Context, svcName string, port int, path string, params map[string]string) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req := rc.f.Clientset.CoreV1().RESTClient().Post().
			Resource("services").
			Namespace(rc.namespace).
			Name(fmt.Sprintf("%s:%d", svcName, port)).
			SubResource("proxy").
			Suffix(path)
		for k, v := range params {
			req = req.Param(k, v)
		}
		_, err := req.DoRaw(ctx)
		if err == nil {
			return
		}
		lastErr = err
		time.Sleep(5 * time.Second)
	}
	fmt.Printf("[ResourceConsumer] %s to %s:%d failed after retries: %v\n", path, svcName, port, lastErr)
}

// waitForServiceEndpoints blocks until the named Service has at least one ready endpoint.
func (f *Framework) waitForServiceEndpoints(ctx context.Context, name, namespace string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		endpoints, err := f.Clientset.CoreV1().Endpoints(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		for _, subset := range endpoints.Subsets {
			if len(subset.Addresses) > 0 {
				return true, nil
			}
		}
		return false, nil
	})
}
