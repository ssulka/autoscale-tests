package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var vpaGVR = schema.GroupVersionResource{
	Group:    "autoscaling.k8s.io",
	Version:  "v1",
	Resource: "verticalpodautoscalers",
}

var vpaControllerGVR = schema.GroupVersionResource{
	Group:    "autoscaling.openshift.io",
	Version:  "v1",
	Resource: "verticalpodautoscalercontrollers",
}

// VPAUpdateMode controls how VPA applies recommendations
type VPAUpdateMode string

const (
	VPAUpdateModeOff      VPAUpdateMode = "Off"
	VPAUpdateModeInitial  VPAUpdateMode = "Initial"
	VPAUpdateModeRecreate VPAUpdateMode = "Recreate"
	VPAUpdateModeAuto     VPAUpdateMode = "Auto"
)

// VPAContainerPolicy defines per-container resource policy for a VPA
type VPAContainerPolicy struct {
	ContainerName    string
	MinAllowed       map[string]string // e.g. {"cpu": "100m", "memory": "64Mi"}
	MaxAllowed       map[string]string
	Mode             string // "Auto" or "Off" (ContainerScalingMode)
	ControlledValues string // "RequestsAndLimits" or "RequestsOnly"
}

// VPAConfig holds parameters for creating a VerticalPodAutoscaler
type VPAConfig struct {
	Name              string
	Namespace         string
	TargetDeployment  string
	UpdateMode        VPAUpdateMode
	MinAllowed        map[string]string // global min
	MaxAllowed        map[string]string // global max
	ContainerPolicies []VPAContainerPolicy
}

// EnsureVPAController creates the VerticalPodAutoscalerController CR named
// "default" in the VPA namespace if it doesn't already exist
func (f *Framework) EnsureVPAController(ctx context.Context) error {
	_, err := f.getDynamicClient().Resource(vpaControllerGVR).Namespace(VPANamespace).Get(ctx, "default", metav1.GetOptions{})
	if err == nil {
		fmt.Printf("[VPA] VerticalPodAutoscalerController 'default' already exists\n")
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check VPA controller CR: %w", err)
	}

	cr := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "autoscaling.openshift.io/v1",
			"kind":       "VerticalPodAutoscalerController",
			"metadata": map[string]interface{}{
				"name":      "default",
				"namespace": VPANamespace,
			},
			"spec": map[string]interface{}{
				"safetyMarginFraction": "0.15",
				"podMinCPUMillicores":  int64(25),
				"podMinMemoryMb":       int64(250),
				"minReplicas":          int64(2),
			},
		},
	}

	_, err = f.getDynamicClient().Resource(vpaControllerGVR).Namespace(VPANamespace).Create(ctx, cr, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to create VPA controller CR: %w", err)
	}
	fmt.Printf("[VPA] Created VerticalPodAutoscalerController 'default' in %s\n", VPANamespace)
	return nil
}

// DeleteVPAController removes the VerticalPodAutoscalerController CR
func (f *Framework) DeleteVPAController(ctx context.Context) error {
	err := f.getDynamicClient().Resource(vpaControllerGVR).Namespace(VPANamespace).Delete(ctx, "default", metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

// WaitForVPAComponentsReady waits until the recommender, admission-plugin,
// and updater deployments are all available in the VPA namespace.
func (f *Framework) WaitForVPAComponentsReady(ctx context.Context, timeout time.Duration) error {
	fmt.Printf("[VPA] Waiting for VPA components (recommender, admission, updater) to be ready...\n")
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		deps := &appsv1.DeploymentList{}
		if err := f.Client.List(ctx, deps, &client.ListOptions{Namespace: VPANamespace}); err != nil {
			return false, nil
		}

		foundRecommender := false
		foundAdmission := false
		foundUpdater := false
		for _, d := range deps.Items {
			if d.Status.AvailableReplicas == 0 {
				continue
			}
			name := d.Name
			if strings.Contains(name, "recommender") {
				foundRecommender = true
			}
			if strings.Contains(name, "admission") {
				foundAdmission = true
			}
			if strings.Contains(name, "updater") {
				foundUpdater = true
			}
		}

		if foundRecommender && foundAdmission && foundUpdater {
			fmt.Printf("[VPA] All VPA components are ready\n")
			return true, nil
		}
		fmt.Printf("[VPA] Components status — recommender=%v, admission=%v, updater=%v\n",
			foundRecommender, foundAdmission, foundUpdater)
		return false, nil
	})
}

// CreateVPA creates a VerticalPodAutoscaler CR via dynamic client
func (f *Framework) CreateVPA(ctx context.Context, cfg VPAConfig) error {
	updateMode := string(cfg.UpdateMode)
	if updateMode == "" {
		updateMode = "Auto"
	}

	spec := map[string]interface{}{
		"targetRef": map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"name":       cfg.TargetDeployment,
		},
		"updatePolicy": map[string]interface{}{
			"updateMode": updateMode,
		},
	}

	if len(cfg.ContainerPolicies) > 0 || cfg.MinAllowed != nil || cfg.MaxAllowed != nil {
		policies := buildResourcePolicy(cfg)
		spec["resourcePolicy"] = policies
	}

	vpa := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "autoscaling.k8s.io/v1",
			"kind":       "VerticalPodAutoscaler",
			"metadata": map[string]interface{}{
				"name":      cfg.Name,
				"namespace": cfg.Namespace,
			},
			"spec": spec,
		},
	}

	_, err := f.getDynamicClient().Resource(vpaGVR).Namespace(cfg.Namespace).Create(ctx, vpa, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to create VPA %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	fmt.Printf("[VPA] VerticalPodAutoscaler %q created in %s\n", cfg.Name, cfg.Namespace)
	return nil
}

func buildResourcePolicy(cfg VPAConfig) map[string]interface{} {
	var containerPolicies []interface{}

	if len(cfg.ContainerPolicies) > 0 {
		for _, cp := range cfg.ContainerPolicies {
			policy := map[string]interface{}{}
			if cp.ContainerName != "" {
				policy["containerName"] = cp.ContainerName
			} else {
				policy["containerName"] = "*"
			}
			if cp.MinAllowed != nil {
				policy["minAllowed"] = toStringInterfaceMapVPA(cp.MinAllowed)
			}
			if cp.MaxAllowed != nil {
				policy["maxAllowed"] = toStringInterfaceMapVPA(cp.MaxAllowed)
			}
			if cp.Mode != "" {
				policy["mode"] = cp.Mode
			}
			if cp.ControlledValues != "" {
				policy["controlledValues"] = cp.ControlledValues
			}
			containerPolicies = append(containerPolicies, policy)
		}
	} else {
		policy := map[string]interface{}{
			"containerName": "*",
		}
		if cfg.MinAllowed != nil {
			policy["minAllowed"] = toStringInterfaceMapVPA(cfg.MinAllowed)
		}
		if cfg.MaxAllowed != nil {
			policy["maxAllowed"] = toStringInterfaceMapVPA(cfg.MaxAllowed)
		}
		containerPolicies = append(containerPolicies, policy)
	}

	return map[string]interface{}{
		"containerPolicies": containerPolicies,
	}
}

// GetVPA retrieves a VPA object by name
func (f *Framework) GetVPA(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	return f.getDynamicClient().Resource(vpaGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// DeleteVPA removes a VPA. Returns nil if already gone
func (f *Framework) DeleteVPA(ctx context.Context, name, namespace string) error {
	err := f.getDynamicClient().Resource(vpaGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

// VPARecommendation holds parsed recommendation values for a single container
type VPARecommendation struct {
	ContainerName  string
	Target         map[string]string // e.g. {"cpu": "250m", "memory": "200Mi"}
	LowerBound     map[string]string
	UpperBound     map[string]string
	UncappedTarget map[string]string
}

// GetVPARecommendations parses the recommendation from a VPA status
func (f *Framework) GetVPARecommendations(ctx context.Context, name, namespace string) ([]VPARecommendation, error) {
	vpa, err := f.GetVPA(ctx, name, namespace)
	if err != nil {
		return nil, err
	}

	recs, found, err := unstructured.NestedSlice(vpa.Object, "status", "recommendation", "containerRecommendations")
	if err != nil || !found || len(recs) == 0 {
		return nil, fmt.Errorf("no recommendations found in VPA %s/%s", namespace, name)
	}

	var result []VPARecommendation
	for _, r := range recs {
		rec, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		containerName, _, _ := unstructured.NestedString(rec, "containerName")
		vr := VPARecommendation{
			ContainerName:  containerName,
			Target:         extractResourceMap(rec, "target"),
			LowerBound:     extractResourceMap(rec, "lowerBound"),
			UpperBound:     extractResourceMap(rec, "upperBound"),
			UncappedTarget: extractResourceMap(rec, "uncappedTarget"),
		}
		result = append(result, vr)
	}
	return result, nil
}

// toStringInterfaceMapVPA converts map[string]string to map[string]interface{}
func toStringInterfaceMapVPA(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func extractResourceMap(rec map[string]interface{}, key string) map[string]string {
	raw, found, _ := unstructured.NestedMap(rec, key)
	if !found {
		return nil
	}
	result := map[string]string{}
	for k, v := range raw {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

func (f *Framework) DeleteVPACheckpoints(ctx context.Context, namespace string) error {
	checkpointGVR := schema.GroupVersionResource{
		Group:    "autoscaling.k8s.io",
		Version:  "v1",
		Resource: "verticalpodautoscalercheckpoints",
	}
	list, err := f.getDynamicClient().Resource(checkpointGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to list VPA checkpoints in %s: %w", namespace, err)
	}
	for _, cp := range list.Items {
		if delErr := f.getDynamicClient().Resource(checkpointGVR).Namespace(namespace).Delete(ctx, cp.GetName(), metav1.DeleteOptions{}); delErr != nil {
			if !errors.IsNotFound(delErr) {
				fmt.Printf("[VPA] Warning: failed to delete checkpoint %s: %v\n", cp.GetName(), delErr)
			}
		}
	}
	if len(list.Items) > 0 {
		fmt.Printf("[VPA] Deleted %d checkpoints in namespace %s\n", len(list.Items), namespace)
	}
	return nil
}

func (f *Framework) WaitForPodMetricsAvailable(ctx context.Context, namespace string, timeout time.Duration) error {
	metricsGVR := schema.GroupVersionResource{
		Group:    "metrics.k8s.io",
		Version:  "v1beta1",
		Resource: "pods",
	}
	fmt.Printf("[VPA] Waiting for pod metrics in namespace %s (timeout %s)...\n", namespace, timeout)
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		list, err := f.getDynamicClient().Resource(metricsGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, nil
		}
		for _, item := range list.Items {
			containers, found, _ := unstructured.NestedSlice(item.Object, "containers")
			if found && len(containers) > 0 {
				fmt.Printf("[VPA] Pod metrics available for %s/%s\n", namespace, item.GetName())
				return true, nil
			}
		}
		return false, nil
	})
}

func (f *Framework) RestartVPARecommender(ctx context.Context, timeout time.Duration) error {
	namespace := VPANamespace
	labelSelector := map[string]string{"app": "vpa-recommender"}

	pods, err := f.ListPods(ctx, namespace, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to list VPA recommender pods: %w", err)
	}
	if len(pods.Items) == 0 {
		fmt.Printf("[VPA] No recommender pods found with label app=vpa-recommender, trying alternative label\n")
		labelSelector = map[string]string{"k8s-app": "vpa-recommender"}
		pods, err = f.ListPods(ctx, namespace, labelSelector)
		if err != nil {
			return fmt.Errorf("failed to list VPA recommender pods: %w", err)
		}
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no VPA recommender pods found in namespace %s", namespace)
	}

	oldUIDs := make(map[string]bool)
	for _, pod := range pods.Items {
		oldUIDs[string(pod.UID)] = true
		fmt.Printf("[VPA] Deleting recommender pod %s (UID %s)\n", pod.Name, pod.UID)
		if delErr := f.DeletePod(ctx, pod.Name, namespace); delErr != nil {
			return fmt.Errorf("failed to delete recommender pod %s: %w", pod.Name, delErr)
		}
	}

	fmt.Printf("[VPA] Waiting for new recommender pod to become ready...\n")
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pods, err := f.ListPods(ctx, namespace, labelSelector)
		if err != nil {
			return false, nil
		}
		for _, pod := range pods.Items {
			if oldUIDs[string(pod.UID)] {
				continue
			}
			if isPodReady(&pod) {
				fmt.Printf("[VPA] New recommender pod %s is ready\n", pod.Name)
				return true, nil
			}
		}
		return false, nil
	})
}

// WaitForVPARecommendation waits until the VPA has at least one container recommendation
func (f *Framework) WaitForVPARecommendation(ctx context.Context, name, namespace string, timeout time.Duration) error {
	fmt.Printf("[VPA] %s: waiting for recommendation (timeout %s)...\n", name, timeout)
	pollCount := 0
	return wait.PollUntilContextTimeout(ctx, 15*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pollCount++
		vpa, err := f.GetVPA(ctx, name, namespace)
		if err != nil {
			return false, nil
		}

		recs, found, _ := unstructured.NestedSlice(vpa.Object, "status", "recommendation", "containerRecommendations")
		if !found || len(recs) == 0 {
			if pollCount%4 == 0 {
				fmt.Printf("[VPA] %s: still waiting (%ds elapsed)...\n", name, pollCount*15)
			}
			return false, nil
		}

		for _, r := range recs {
			rec, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			target, tFound, _ := unstructured.NestedMap(rec, "target")
			if tFound && len(target) > 0 {
				fmt.Printf("[VPA] %s: recommendation available — %v\n", name, target)
				return true, nil
			}
		}
		return false, nil
	})
}

// ScaleVPARecommender scales the VPA recommender deployment to the desired
// replica count. Use replicas=0 to pause the recommender and replicas=1 to resume it
func (f *Framework) ScaleVPARecommender(ctx context.Context, replicas int32, timeout time.Duration) error {
	depName, err := f.findVPARecommenderDeployment(ctx)
	if err != nil {
		return err
	}

	dep, err := f.GetDeployment(ctx, depName, VPANamespace)
	if err != nil {
		return fmt.Errorf("failed to get VPA recommender deployment %s: %w", depName, err)
	}
	dep.Spec.Replicas = &replicas
	if err := f.Client.Update(ctx, dep); err != nil {
		return fmt.Errorf("failed to scale VPA recommender to %d: %w", replicas, err)
	}
	fmt.Printf("[VPA] Scaled recommender deployment %s to %d replicas\n", depName, replicas)

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		d, err := f.GetDeployment(ctx, depName, VPANamespace)
		if err != nil {
			return false, nil
		}
		if replicas == 0 {
			return d.Status.AvailableReplicas == 0, nil
		}
		return d.Status.AvailableReplicas >= replicas, nil
	})
}

func (f *Framework) findVPARecommenderDeployment(ctx context.Context) (string, error) {
	candidates := []string{
		"vpa-recommender-default",
		"vpa-recommender",
		"vertical-pod-autoscaler-recommender",
	}
	for _, name := range candidates {
		_, err := f.GetDeployment(ctx, name, VPANamespace)
		if err == nil {
			return name, nil
		}
	}

	deps := &appsv1.DeploymentList{}
	if err := f.Client.List(ctx, deps, &client.ListOptions{Namespace: VPANamespace}); err != nil {
		return "", fmt.Errorf("failed to list deployments in %s: %w", VPANamespace, err)
	}
	for _, d := range deps.Items {
		if strings.Contains(d.Name, "recommender") {
			fmt.Printf("[VPA] Found recommender deployment by name match: %s\n", d.Name)
			return d.Name, nil
		}
	}
	return "", fmt.Errorf("could not find VPA recommender deployment in namespace %s", VPANamespace)
}

// SetVPARecommendation patches the VPA status with a synthetic recommendation
// After patching, it verifies that the recommendation is readable from the API
// before returning — this avoids race conditions with the admission webhook
func (f *Framework) SetVPARecommendation(ctx context.Context, name, namespace string, containerName string, cpuTarget, memTarget string) error {
	patch := map[string]interface{}{
		"status": map[string]interface{}{
			"recommendation": map[string]interface{}{
				"containerRecommendations": []interface{}{
					map[string]interface{}{
						"containerName": containerName,
						"target": map[string]interface{}{
							"cpu":    cpuTarget,
							"memory": memTarget,
						},
						"lowerBound": map[string]interface{}{
							"cpu":    cpuTarget,
							"memory": memTarget,
						},
						"upperBound": map[string]interface{}{
							"cpu":    cpuTarget,
							"memory": memTarget,
						},
						"uncappedTarget": map[string]interface{}{
							"cpu":    cpuTarget,
							"memory": memTarget,
						},
					},
				},
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal VPA status patch: %w", err)
	}

	_, err = f.getDynamicClient().Resource(vpaGVR).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{}, "status",
	)
	if err != nil {
		return fmt.Errorf("failed to patch VPA %s/%s status: %w", namespace, name, err)
	}
	fmt.Printf("[VPA] Set synthetic recommendation on %s: CPU=%s, Mem=%s\n", name, cpuTarget, memTarget)

	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		vpa, err := f.GetVPA(ctx, name, namespace)
		if err != nil {
			return false, nil
		}
		recs, found, _ := unstructured.NestedSlice(vpa.Object, "status", "recommendation", "containerRecommendations")
		if !found || len(recs) == 0 {
			return false, nil
		}
		for _, r := range recs {
			rec, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			target, tFound, _ := unstructured.NestedMap(rec, "target")
			if tFound && len(target) > 0 {
				fmt.Printf("[VPA] Verified recommendation readable: %v\n", target)
				return true, nil
			}
		}
		return false, nil
	})
}

// WaitForPodEviction waits until at least one pod from the original set has been replaced
// (detected by UID change). Returns nil when eviction is detected
func (f *Framework) WaitForPodEviction(ctx context.Context, namespace string, labelSelector map[string]string, originalUIDs map[types.UID]bool, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pods, err := f.ListPods(ctx, namespace, labelSelector)
		if err != nil {
			return false, nil
		}
		for _, pod := range pods.Items {
			if !originalUIDs[pod.UID] {
				fmt.Printf("[VPA] Eviction detected: new pod %s (UID %s)\n", pod.Name, pod.UID)
				return true, nil
			}
		}
		return false, nil
	})
}

// GetPodUIDs returns a set of UIDs for all pods matching the label selector
func (f *Framework) GetPodUIDs(ctx context.Context, namespace string, labelSelector map[string]string) (map[types.UID]bool, error) {
	pods, err := f.ListPods(ctx, namespace, labelSelector)
	if err != nil {
		return nil, err
	}
	uids := make(map[types.UID]bool, len(pods.Items))
	for _, pod := range pods.Items {
		uids[pod.UID] = true
	}
	return uids, nil
}
