package framework

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

var scaledObjectGVR = schema.GroupVersionResource{
	Group:    "keda.sh",
	Version:  "v1alpha1",
	Resource: "scaledobjects",
}

// ScaledObjectTrigger represents a single KEDA trigger
type ScaledObjectTrigger struct {
	Type       string            // e.g. "cpu", "cron", "memory"
	MetricType string            // e.g. "Utilization", "AverageValue" (optional, used by cpu/memory)
	Metadata   map[string]string // trigger-specific key-value pairs
}

// ScaledObjectConfig holds parameters for creating a ScaledObject.
type ScaledObjectConfig struct {
	Name            string
	Namespace       string
	DeploymentName  string
	MinReplicas     *int64 // nil = KEDA default (0 for scale-to-zero)
	MaxReplicas     int64
	PollingInterval *int64 // seconds between trigger checks (nil = KEDA default 30s)
	CooldownPeriod  *int64 // seconds after last trigger before scaling to minReplicas (nil = KEDA default 300s)
	Triggers        []ScaledObjectTrigger

	// HPA behavior overrides (optional)
	ScaleDownStabilizationSeconds *int64
}

// CreateScaledObject creates a KEDA ScaledObject using the dynamic client.
func (f *Framework) CreateScaledObject(ctx context.Context, cfg ScaledObjectConfig) error {
	triggers := make([]interface{}, 0, len(cfg.Triggers))
	for _, t := range cfg.Triggers {
		trigger := map[string]interface{}{
			"type":     t.Type,
			"metadata": toStringInterfaceMap(t.Metadata),
		}
		if t.MetricType != "" {
			trigger["metricType"] = t.MetricType
		}
		triggers = append(triggers, trigger)
	}

	spec := map[string]interface{}{
		"scaleTargetRef": map[string]interface{}{
			"name": cfg.DeploymentName,
		},
		"maxReplicaCount": cfg.MaxReplicas,
		"triggers":        triggers,
	}

	if cfg.MinReplicas != nil {
		spec["minReplicaCount"] = *cfg.MinReplicas
	}
	if cfg.PollingInterval != nil {
		spec["pollingInterval"] = *cfg.PollingInterval
	}
	if cfg.CooldownPeriod != nil {
		spec["cooldownPeriod"] = *cfg.CooldownPeriod
	}

	if cfg.ScaleDownStabilizationSeconds != nil {
		spec["advanced"] = map[string]interface{}{
			"horizontalPodAutoscalerConfig": map[string]interface{}{
				"behavior": map[string]interface{}{
					"scaleDown": map[string]interface{}{
						"stabilizationWindowSeconds": *cfg.ScaleDownStabilizationSeconds,
					},
				},
			},
		}
	}

	so := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "keda.sh/v1alpha1",
			"kind":       "ScaledObject",
			"metadata": map[string]interface{}{
				"name":      cfg.Name,
				"namespace": cfg.Namespace,
			},
			"spec": spec,
		},
	}

	_, err := f.Clientset.Discovery().ServerResourcesForGroupVersion("keda.sh/v1alpha1")
	if err != nil {
		return fmt.Errorf("KEDA API (keda.sh/v1alpha1) not available — is the CMA operator installed? %w", err)
	}

	result, err := f.getDynamicClient().Resource(scaledObjectGVR).Namespace(cfg.Namespace).Create(ctx, so, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ScaledObject %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	fmt.Printf("[KEDA] ScaledObject %q created in %s\n", result.GetName(), cfg.Namespace)
	return nil
}

// GetScaledObject retrieves a ScaledObject by name.
func (f *Framework) GetScaledObject(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	return f.getDynamicClient().Resource(scaledObjectGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// DeleteScaledObject removes a ScaledObject. Returns nil if already gone.
func (f *Framework) DeleteScaledObject(ctx context.Context, name, namespace string) error {
	err := f.getDynamicClient().Resource(scaledObjectGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

// CreateScaledObjectRaw creates a ScaledObject from a raw unstructured map.
// Useful for validation tests where you need malformed or edge-case objects.
func (f *Framework) CreateScaledObjectRaw(ctx context.Context, namespace string, obj *unstructured.Unstructured) error {
	_, err := f.getDynamicClient().Resource(scaledObjectGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	return err
}

// WaitForScaledObjectReady waits until the ScaledObject's status shows it's active.
func (f *Framework) WaitForScaledObjectReady(ctx context.Context, name, namespace string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		so, err := f.GetScaledObject(ctx, name, namespace)
		if err != nil {
			return false, nil
		}
		conditions, found, _ := unstructured.NestedSlice(so.Object, "status", "conditions")
		if !found {
			return false, nil
		}
		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if cond["type"] == "Ready" && cond["status"] == "True" {
				return true, nil
			}
		}
		return false, nil
	})
}

// WaitForKEDAScaleUp waits until the deployment managed by a ScaledObject reaches
// at least minReplicas ready replicas.
func (f *Framework) WaitForKEDAScaleUp(ctx context.Context, deploymentName, namespace string, minReplicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		dep, err := f.GetDeployment(ctx, deploymentName, namespace)
		if err != nil {
			return false, nil
		}
		current := dep.Status.ReadyReplicas
		fmt.Printf("[KEDA] %s: readyReplicas=%d, waiting for >=%d\n", deploymentName, current, minReplicas)
		return current >= minReplicas, nil
	})
}

// WaitForKEDAScaleDown waits until the deployment managed by a ScaledObject scales
// down to at most maxReplicas ready replicas.
func (f *Framework) WaitForKEDAScaleDown(ctx context.Context, deploymentName, namespace string, maxReplicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		dep, err := f.GetDeployment(ctx, deploymentName, namespace)
		if err != nil {
			return false, nil
		}
		current := dep.Status.ReadyReplicas
		fmt.Printf("[KEDA] %s: readyReplicas=%d, waiting for <=%d\n", deploymentName, current, maxReplicas)
		return current <= maxReplicas, nil
	})
}

// EnsureDeploymentReplicasStable verifies the replica count stays within [min, max] for duration.
func (f *Framework) EnsureDeploymentReplicasStable(ctx context.Context, deploymentName, namespace string, min, max int32, duration time.Duration) error {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		dep, err := f.GetDeployment(ctx, deploymentName, namespace)
		if err != nil {
			return fmt.Errorf("failed to get deployment %s: %w", deploymentName, err)
		}
		current := dep.Status.ReadyReplicas
		if current < min || current > max {
			return fmt.Errorf("deployment %s replicas %d out of range [%d, %d]", deploymentName, current, min, max)
		}
		time.Sleep(10 * time.Second)
	}
	return nil
}

// PauseScaledObject sets the KEDA pause annotation on a ScaledObject.
// KEDA will stop scaling the target while paused.
func (f *Framework) PauseScaledObject(ctx context.Context, name, namespace string, pausedReplicas int) error {
	so, err := f.GetScaledObject(ctx, name, namespace)
	if err != nil {
		return fmt.Errorf("failed to get ScaledObject: %w", err)
	}
	annotations := so.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["autoscaling.keda.sh/paused-replicas"] = fmt.Sprintf("%d", pausedReplicas)
	so.SetAnnotations(annotations)
	_, err = f.getDynamicClient().Resource(scaledObjectGVR).Namespace(namespace).Update(ctx, so, metav1.UpdateOptions{})
	return err
}

// ResumeScaledObject removes the KEDA pause annotation from a ScaledObject
func (f *Framework) ResumeScaledObject(ctx context.Context, name, namespace string) error {
	so, err := f.GetScaledObject(ctx, name, namespace)
	if err != nil {
		return fmt.Errorf("failed to get ScaledObject: %w", err)
	}
	annotations := so.GetAnnotations()
	delete(annotations, "autoscaling.keda.sh/paused-replicas")
	so.SetAnnotations(annotations)
	_, err = f.getDynamicClient().Resource(scaledObjectGVR).Namespace(namespace).Update(ctx, so, metav1.UpdateOptions{})
	return err
}

// WaitForDeploymentReplicas waits until a deployment has exactly the specified number of ready replicas
func (f *Framework) WaitForKEDAExactReplicas(ctx context.Context, deploymentName, namespace string, replicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		dep, err := f.GetDeployment(ctx, deploymentName, namespace)
		if err != nil {
			return false, nil
		}
		current := dep.Status.ReadyReplicas
		fmt.Printf("[KEDA] %s: readyReplicas=%d, waiting for ==%d\n", deploymentName, current, replicas)
		return current == replicas, nil
	})
}

// toStringInterfaceMap converts map[string]string to map[string]interface{} for unstructured objects.
func toStringInterfaceMap(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
