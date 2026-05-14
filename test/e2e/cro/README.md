# CRO (Cluster Resource Override) E2E Tests

End-to-end tests for the Cluster Resource Override admission operator on OpenShift.

## Prerequisites

- OpenShift cluster with OLM (Operator Lifecycle Manager)
- CRO operator available in `redhat-operators` catalog

## How CRO Works

CRO deploys a mutating admission webhook that adjusts pod CPU/memory requests and limits
based on configured ratios. Namespaces must opt-in with the label:

```
clusterresourceoverrides.admission.autoscaling.openshift.io/enabled=true
```

Configuration ratios:
- **LimitCPUToMemoryPercent**: CPU limit = memory limit (in Mi) × percent / 100 (as millicores)
- **CPURequestToLimitPercent**: CPU request = CPU limit × percent / 100
- **MemoryRequestToLimitPercent**: Memory request = memory limit × percent / 100

## Test Scenarios

### Installation verification
- CRO operator namespace exists (`clusterresourceoverride-operator`)
- CRO operator pods are running
- All CRO operator pods are in Ready state

### Resource override with opt-in
- Single container: CRO adjusts CPU limit, CPU request, and memory request
- Multiple containers: CRO applies overrides independently to each container

### Init container override
- CRO overrides resources on init containers the same as regular containers

### LimitRange with default limits
- Pod without resource specs gets defaults from LimitRange, then CRO applies overrides

### No opt-in namespace
- CRO does NOT modify pod resources in a namespace without the opt-in label

### Configuration change
- After updating CRO ratios, new pods receive the updated values

### LimitRange interaction
- CRO respects namespace LimitRange maximums (CPU limit clamped to LimitRange max)

### Deployment verification
- CRO webhook deployment is running with ready replicas

## Running

```bash
make test-e2e-cro
# or
go test -v ./test/e2e/cro/ -timeout 30m
```
