# CMA (Custom Metrics Autoscaler) E2E Tests

End-to-end tests for the Custom Metrics Autoscaler (KEDA) operator on OpenShift.

## Prerequisites

- OpenShift cluster with OLM (Operator Lifecycle Manager)
- CMA operator available in `redhat-operators` catalog
- `resource-consumer` image accessible (`registry.k8s.io/e2e-test-images/resource-consumer:1.13`)

## Test Scenarios

### Installation verification
- CMA operator namespace exists (`openshift-keda`)
- CMA operator pod is running and ready
- All CMA operator pods are in Ready state

### KEDA components verification
- `keda-operator` pod is running and ready
- `keda-metrics-apiserver` pod is running and ready
- `keda-admission` webhooks pod is running and ready

### KEDA CRD verification
- `ScaledObject` CRD is registered
- `ScaledJob` CRD is registered
- `TriggerAuthentication` CRD is registered

### Cron scaler
- Scale out to 4 replicas during a cron time window
- Scale back in to 1 replica after the cron window ends

### CPU scaler
- Scale out to at least 2 replicas under CPU load (500m total, 50% target)
- Scale back in to 1 replica when load stops

### Memory scaler
- Scale out to at least 2 replicas under memory load (256MB, 50% target of 128Mi)
- Scale back in to 1 replica when load stops

### Scale to zero
- Deploy with 1 replica and attach ScaledObject with `minReplicas=0` and inactive trigger
- Verify KEDA scales deployment to 0 replicas

### Paused ScaledObject
- Scale out to 4 replicas via active cron trigger
- Pause ScaledObject at 2 replicas — verify deployment holds at 2
- Resume ScaledObject — verify KEDA scales back up to 4

### ScaledObject validation
- Reject a second ScaledObject targeting the same deployment
- Reject a ScaledObject with CPU trigger when the deployment has no CPU requests

## Running

```bash
make test-e2e-cma
# or
go test -v ./test/e2e/cma/ -timeout 45m
```
