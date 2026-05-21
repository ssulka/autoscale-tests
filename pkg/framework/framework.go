package framework

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"text/template"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	rt "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObjectKey is an alias for types.NamespacedName for convenience in tests
type ObjectKey = types.NamespacedName

// Framework Helpers

type Framework struct {
	Client     client.Client
	Clientset  *kubernetes.Clientset
	RestConfig *rest.Config
	Ctx        context.Context
	Namespace  string
}

func NewFramework() (*Framework, error) {
	config, err := getConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	scheme := newScheme()
	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &Framework{
		Client:     c,
		Clientset:  clientset,
		RestConfig: config,
		Ctx:        context.Background(),
		Namespace:  getNamespace(),
	}, nil
}

func newScheme() *rt.Scheme {
	scheme := rt.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(autoscalingv1.AddToScheme(scheme))
	utilruntime.Must(autoscalingv2.AddToScheme(scheme))
	return scheme
}

func getConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}

func getNamespace() string {
	if ns := os.Getenv("TEST_NAMESPACE"); ns != "" {
		return ns
	}
	return "default"
}

func (f *Framework) WithTimeout(timeout time.Duration) context.Context {
	ctx, _ := context.WithTimeout(f.Ctx, timeout)
	return ctx
}

// Pod Helpers

func (f *Framework) GetPod(ctx context.Context, name, namespace string) (*corev1.Pod, error) {
	pod := &corev1.Pod{}
	err := f.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, pod)
	return pod, err
}

func (f *Framework) ListPods(ctx context.Context, namespace string, labelSelector map[string]string) (*corev1.PodList, error) {
	podList := &corev1.PodList{}
	err := f.Client.List(ctx, podList, &client.ListOptions{
		Namespace:     namespace,
		LabelSelector: labels.SelectorFromSet(labelSelector),
	})
	return podList, err
}

func (f *Framework) WaitForPodReady(ctx context.Context, name, namespace string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := f.GetPod(ctx, name, namespace)
		if err != nil {
			return false, nil
		}
		return isPodReady(pod), nil
	})
}

func (f *Framework) WaitForPodsWithLabel(ctx context.Context, namespace string, labelSelector map[string]string, minReady int, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pods, err := f.ListPods(ctx, namespace, labelSelector)
		if err != nil {
			return false, nil
		}
		readyCount := 0
		for _, pod := range pods.Items {
			if isPodReady(&pod) {
				readyCount++
			}
		}
		return readyCount >= minReady, nil
	})
}

func (f *Framework) ExecInPod(ctx context.Context, namespace, podName, containerName string, command []string) (string, string, error) {
	req := f.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").Name(podName).Namespace(namespace).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, clientgoscheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(f.RestConfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr})
	return stdout.String(), stderr.String(), err
}

func (f *Framework) GetPodLogs(ctx context.Context, namespace, podName, containerName string, tailLines int64) (string, error) {
	req := f.Clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		TailLines: &tailLines,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, stream)
	return buf.String(), err
}

func (f *Framework) DeletePod(ctx context.Context, name, namespace string) error {
	return f.Client.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// Namespace Helpers

func (f *Framework) NamespaceExists(ctx context.Context, name string) (bool, error) {
	err := f.Client.Get(ctx, client.ObjectKey{Name: name}, &corev1.Namespace{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (f *Framework) CreateNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"test-suite": "autoscale-tests"},
		},
	}
	err := f.Client.Create(ctx, ns)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ns, nil
		}
		return nil, fmt.Errorf("failed to create namespace: %w", err)
	}
	return ns, nil
}

func (f *Framework) DeleteNamespace(ctx context.Context, name string) error {
	return client.IgnoreNotFound(f.Client.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}))
}

func (f *Framework) CreateTestNamespace(ctx context.Context, prefix string) (string, error) {
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	_, err := f.CreateNamespace(ctx, name)
	return name, err
}

func (f *Framework) CleanupTestNamespace(ctx context.Context, name string) error {
	if err := f.DeleteNamespace(ctx, name); err != nil {
		return err
	}
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		exists, err := f.NamespaceExists(ctx, name)
		return !exists, err
	})
}

// Deployment Helpers

type ContainerConfig struct {
	Name          string
	Image         string
	Command       []string
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string
}

type DeploymentConfig struct {
	Name              string
	Namespace         string
	Replicas          int32
	Image             string
	Command           []string
	CPURequest        string
	MemoryRequest     string
	CPULimit          string
	MemoryLimit       string
	Labels            map[string]string
	ExtraContainers   []ContainerConfig
}

func DefaultDeploymentConfig(name, namespace string) DeploymentConfig {
	return DeploymentConfig{
		Name:          name,
		Namespace:     namespace,
		Replicas:      1,
		Image:         "registry.k8s.io/pause:3.9",
		CPURequest:    "50m",
		MemoryRequest: "64Mi",
		CPULimit:      "100m",
		MemoryLimit:   "128Mi",
		Labels:        map[string]string{"app": name},
	}
}

func (f *Framework) CreateDeployment(ctx context.Context, cfg DeploymentConfig) (*appsv1.Deployment, error) {
	if cfg.Labels == nil {
		cfg.Labels = map[string]string{"app": cfg.Name}
	}
	containers := []corev1.Container{{
		Name:    "test-container",
		Image:   cfg.Image,
		Command: cfg.Command,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cfg.CPURequest),
				corev1.ResourceMemory: resource.MustParse(cfg.MemoryRequest),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cfg.CPULimit),
				corev1.ResourceMemory: resource.MustParse(cfg.MemoryLimit),
			},
		},
	}}
	for _, ec := range cfg.ExtraContainers {
		containers = append(containers, corev1.Container{
			Name:    ec.Name,
			Image:   ec.Image,
			Command: ec.Command,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(ec.CPURequest),
					corev1.ResourceMemory: resource.MustParse(ec.MemoryRequest),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(ec.CPULimit),
					corev1.ResourceMemory: resource.MustParse(ec.MemoryLimit),
				},
			},
		})
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace, Labels: cfg.Labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &cfg.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: cfg.Labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: cfg.Labels},
				Spec: corev1.PodSpec{
					Containers: containers,
				},
			},
		},
	}
	err := f.Client.Create(ctx, deployment)
	return deployment, err
}

func (f *Framework) GetDeployment(ctx context.Context, name, namespace string) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := f.Client.Get(ctx, ObjectKey{Name: name, Namespace: namespace}, deployment)
	return deployment, err
}

func (f *Framework) DeleteDeployment(ctx context.Context, name, namespace string) error {
	return f.Client.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
}

func (f *Framework) WaitForDeploymentReady(ctx context.Context, name, namespace string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := f.GetDeployment(ctx, name, namespace)
		if err != nil {
			return false, nil
		}
		return deployment.Status.ReadyReplicas == *deployment.Spec.Replicas, nil
	})
}

func (f *Framework) WaitForDeploymentReplicas(ctx context.Context, name, namespace string, replicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := f.GetDeployment(ctx, name, namespace)
		if err != nil {
			return false, nil
		}
		return deployment.Status.ReadyReplicas >= replicas, nil
	})
}

// HPA Helpers

type HPAConfig struct {
	Name                              string
	Namespace                         string
	TargetDeployment                  string
	MinReplicas                       int32
	MaxReplicas                       int32
	CPUTargetUtilization              *int32
	CPUAverageValue                   string
	MemoryTargetUtilization           *int32
	MemoryAverageValue                string
	// ContainerResource metrics (targets a specific container by name)
	ContainerName                     string
	ContainerCPUTargetUtilization     *int32
	ContainerCPUAverageValue          string
	ContainerMemoryTargetUtilization  *int32
	ContainerMemoryAverageValue       string
	ScaleDownStabilizationWindowSecs  *int32
}

func DefaultHPAConfig(name, namespace, targetDeployment string) HPAConfig {
	cpuTarget := int32(50)
	return HPAConfig{
		Name:                 name,
		Namespace:            namespace,
		TargetDeployment:     targetDeployment,
		MinReplicas:          1,
		MaxReplicas:          5,
		CPUTargetUtilization: &cpuTarget,
	}
}

func (f *Framework) CreateHPA(ctx context.Context, cfg HPAConfig) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       cfg.TargetDeployment,
			},
			MinReplicas: &cfg.MinReplicas,
			MaxReplicas: cfg.MaxReplicas,
			Metrics:     []autoscalingv2.MetricSpec{},
		},
	}
	if cfg.ScaleDownStabilizationWindowSecs != nil {
		periodSecs := int32(15)
		hpa.Spec.Behavior = &autoscalingv2.HorizontalPodAutoscalerBehavior{
			ScaleDown: &autoscalingv2.HPAScalingRules{
				StabilizationWindowSeconds: cfg.ScaleDownStabilizationWindowSecs,
				Policies: []autoscalingv2.HPAScalingPolicy{
					{
						Type:          autoscalingv2.PercentScalingPolicy,
						Value:         100,
						PeriodSeconds: periodSecs,
					},
				},
			},
		}
	}

	if cfg.CPUTargetUtilization != nil {
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: cfg.CPUTargetUtilization,
				},
			},
		})
	}
	if cfg.CPUAverageValue != "" {
		q := resource.MustParse(cfg.CPUAverageValue)
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:         autoscalingv2.AverageValueMetricType,
					AverageValue: &q,
				},
			},
		})
	}
	if cfg.MemoryTargetUtilization != nil {
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceMemory,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: cfg.MemoryTargetUtilization,
				},
			},
		})
	}
	if cfg.MemoryAverageValue != "" {
		q := resource.MustParse(cfg.MemoryAverageValue)
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceMemory,
				Target: autoscalingv2.MetricTarget{
					Type:         autoscalingv2.AverageValueMetricType,
					AverageValue: &q,
				},
			},
		})
	}

	containerName := cfg.ContainerName
	if containerName == "" {
		containerName = "test-container"
	}
	if cfg.ContainerCPUTargetUtilization != nil {
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ContainerResourceMetricSourceType,
			ContainerResource: &autoscalingv2.ContainerResourceMetricSource{
				Name:      corev1.ResourceCPU,
				Container: containerName,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: cfg.ContainerCPUTargetUtilization,
				},
			},
		})
	}
	if cfg.ContainerCPUAverageValue != "" {
		q := resource.MustParse(cfg.ContainerCPUAverageValue)
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ContainerResourceMetricSourceType,
			ContainerResource: &autoscalingv2.ContainerResourceMetricSource{
				Name:      corev1.ResourceCPU,
				Container: containerName,
				Target: autoscalingv2.MetricTarget{
					Type:         autoscalingv2.AverageValueMetricType,
					AverageValue: &q,
				},
			},
		})
	}
	if cfg.ContainerMemoryTargetUtilization != nil {
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ContainerResourceMetricSourceType,
			ContainerResource: &autoscalingv2.ContainerResourceMetricSource{
				Name:      corev1.ResourceMemory,
				Container: containerName,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: cfg.ContainerMemoryTargetUtilization,
				},
			},
		})
	}
	if cfg.ContainerMemoryAverageValue != "" {
		q := resource.MustParse(cfg.ContainerMemoryAverageValue)
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ContainerResourceMetricSourceType,
			ContainerResource: &autoscalingv2.ContainerResourceMetricSource{
				Name:      corev1.ResourceMemory,
				Container: containerName,
				Target: autoscalingv2.MetricTarget{
					Type:         autoscalingv2.AverageValueMetricType,
					AverageValue: &q,
				},
			},
		})
	}

	err := f.Client.Create(ctx, hpa)
	return hpa, err
}

func (f *Framework) GetHPA(ctx context.Context, name, namespace string) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	err := f.Client.Get(ctx, ObjectKey{Name: name, Namespace: namespace}, hpa)
	return hpa, err
}

func (f *Framework) DeleteHPA(ctx context.Context, name, namespace string) error {
	return f.Client.Delete(ctx, &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
}

func (f *Framework) WaitForHPAScaleUp(ctx context.Context, hpaName, namespace string, minReplicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		hpa, err := f.GetHPA(ctx, hpaName, namespace)
		if err != nil {
			return false, nil
		}
		return hpa.Status.CurrentReplicas > minReplicas, nil
	})
}

func (f *Framework) WaitForHPAScaleDown(ctx context.Context, hpaName, namespace string, targetReplicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		hpa, err := f.GetHPA(ctx, hpaName, namespace)
		if err != nil {
			return false, nil
		}
		return hpa.Status.CurrentReplicas <= targetReplicas, nil
	})
}

func (f *Framework) WaitForHPAReplicas(ctx context.Context, hpaName, namespace string, targetReplicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 20*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		hpa, err := f.GetHPA(ctx, hpaName, namespace)
		if err != nil {
			return false, nil
		}
		current := hpa.Status.CurrentReplicas
		fmt.Printf("[HPA] %s: currentReplicas=%d, desiredReplicas=%d, target=%d\n",
			hpaName, current, hpa.Status.DesiredReplicas, targetReplicas)
		return current == targetReplicas, nil
	})
}

func (f *Framework) WaitForHPAScaleAtLeast(ctx context.Context, hpaName, namespace string, minReplicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 20*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		hpa, err := f.GetHPA(ctx, hpaName, namespace)
		if err != nil {
			return false, nil
		}
		current := hpa.Status.CurrentReplicas
		fmt.Printf("[HPA] %s: currentReplicas=%d, waiting for >=%d\n",
			hpaName, current, minReplicas)
		return current >= minReplicas, nil
	})
}

func (f *Framework) WaitForHPAScaleAtMost(ctx context.Context, hpaName, namespace string, maxReplicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 20*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		hpa, err := f.GetHPA(ctx, hpaName, namespace)
		if err != nil {
			return false, nil
		}
		current := hpa.Status.CurrentReplicas
		fmt.Printf("[HPA] %s: currentReplicas=%d, waiting for <=%d\n",
			hpaName, current, maxReplicas)
		return current <= maxReplicas, nil
	})
}

func (f *Framework) EnsureHPAReplicasInRange(ctx context.Context, hpaName, namespace string, min, max int32, duration time.Duration) error {
	// Wait for HPA to become active (CurrentReplicas > 0) before checking stability.
	// HPA controller needs time to read metrics and set CurrentReplicas.
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		hpa, err := f.GetHPA(ctx, hpaName, namespace)
		if err != nil {
			return false, nil
		}
		if hpa.Status.CurrentReplicas > 0 {
			fmt.Printf("[HPA] %s: became active with %d replicas\n", hpaName, hpa.Status.CurrentReplicas)
			return true, nil
		}
		fmt.Printf("[HPA] %s: waiting for CurrentReplicas > 0 (currently %d)\n", hpaName, hpa.Status.CurrentReplicas)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("HPA %s never became active: %w", hpaName, err)
	}

	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		hpa, err := f.GetHPA(ctx, hpaName, namespace)
		if err != nil {
			return fmt.Errorf("failed to get HPA %s: %w", hpaName, err)
		}
		current := hpa.Status.CurrentReplicas
		if current < min || current > max {
			return fmt.Errorf("HPA %s replicas %d out of range [%d, %d]", hpaName, current, min, max)
		}
		time.Sleep(10 * time.Second)
	}
	return nil
}

// Metrics API Helpers

func (f *Framework) MetricsAPIAvailable(ctx context.Context) (bool, error) {
	return f.CRDExists(ctx, "metrics.k8s.io", "PodMetrics")
}

// CRD Helpers

func (f *Framework) CRDExists(ctx context.Context, group, kind string) (bool, error) {
	mapper := f.Client.RESTMapper()
	_, err := mapper.RESTMappings(schema.GroupKind{Group: group, Kind: kind})
	if err != nil {
		if meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Templates

// HPATemplateConfig holds data for rendering hpa_template.yaml
type HPATemplateConfig struct {
	ResourceName               string
	Namespace                  string
	DeploymentName             string
	MinReplicas                int32
	MaxReplicas                int32
	Metrics                    []HPAMetric
	StabilizationWindowSeconds int
}

// HPAMetric represents a single metric in the HPA template
type HPAMetric struct {
	Name         string
	AverageValue string
}

// RenderHPATemplate renders hpa_template.yaml
func RenderHPATemplate(cfg HPATemplateConfig) (string, error) {
	return RenderTemplate("hpa_template.yaml", cfg)
}

// RenderTemplate renders a Go template file from testdata/ with the given data
func RenderTemplate(templateFile string, data interface{}) (string, error) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "..", "..")
	path := filepath.Join(root, "testdata", templateFile)

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read template %s: %w", path, err)
	}

	tmpl, err := template.New(templateFile).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("failed to parse template %s: %w", templateFile, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template %s: %w", templateFile, err)
	}

	return buf.String(), nil
}
