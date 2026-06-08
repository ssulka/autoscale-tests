# VPA (Vertical Pod Autoscaler) E2E Tests

End-to-end tests for the Vertical Pod Autoscaler operator on OpenShift.

## Prerequisites

- OpenShift cluster with OLM (Operator Lifecycle Manager)
- VPA operator available in `redhat-operators` catalog
- `resource-consumer` image accessible (`registry.k8s.io/e2e-test-images/resource-consumer:1.13`)

## Test Scenarios

### Installation verification
- VPA operator namespace exists (`openshift-vertical-pod-autoscaler`)
- VPA operator pods are running and ready
- `VerticalPodAutoscaler` CRD is registered
- `VerticalPodAutoscalerCheckpoint` CRD is registered

### Recommender
- **Serves recommendation**: VPA + Deployment with CPU load produces `status.recommendation`
- **Respects minAllowed**: Target CPU is clamped to at least `minAllowed` value
- **Respects maxAllowed**: Target CPU is capped to at most `maxAllowed` value
- **Multi-container opt-out**: Container with `mode=Off` gets no recommendation, other container does

### Admission Controller
- **Applies recommendation**: Pod starts with requests matching VPA synthetic recommendation
- **Keeps limits-to-request ratio**: Original 2x ratio is maintained after VPA adjusts requests
- **Caps to maxAllowed**: Recommendation above max — pod request capped to `maxAllowed`
- **Raises to minAllowed**: Recommendation below min — pod request raised to `minAllowed`
- **No recommendation passthrough**: VPA without recommendation — pod keeps original requests

### Updater
- **Evicts pods for upscaling**: Recommendation significantly higher than current requests triggers pod eviction and restart with updated requests

## Running

```bash
make test-e2e-vpa
# or
go test -v ./test/e2e/vpa/ -timeout 45m
```
