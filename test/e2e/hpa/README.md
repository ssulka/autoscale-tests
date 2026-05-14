# HPA (Horizontal Pod Autoscaler) E2E Tests

End-to-end tests for the Horizontal Pod Autoscaler on OpenShift.

## Prerequisites

- OpenShift cluster with Metrics API available (`metrics.k8s.io/PodMetrics`)
- `resource-consumer` image accessible (`registry.k8s.io/e2e-test-images/resource-consumer:1.13`)

## Test Scenarios

### Prerequisites
- Verifies Metrics API is available on the cluster

### CPU-based scaling — Deployment (Pod Resource)
- Scale up 1→3→5 pods using Average Utilization
- Scale down 5→3→1 pods using Average Utilization
- Scale up 1→3→5 pods using Average Value

### CPU-based scaling — Deployment (Container Resource)
- Scale up 1→3→5 pods using Average Utilization (targeting specific container)
- Scale up 1→3→5 pods using Average Value (targeting specific container)

### Memory-based scaling — Deployment (Pod Resource)
- Scale up 1→3→5 pods using Average Utilization
- Scale up 1→3→5 pods using Average Value

### Memory-based scaling — Deployment (Container Resource)
- Scale up 1→3→5 pods using Average Utilization (targeting specific container)
- Scale up 1→3→5 pods using Average Value (targeting specific container)

### Deployment light (fast CPU scale test)
- Scale up 1→2 pods on CPU load
- Scale down 2→1 pods when load stops

### Deployment with idle sidecar (ContainerResource use case)
- Scale up on a busy application with an idle sidecar container
- Should NOT scale up on a busy sidecar with an idle application

## Running

```bash
make test-e2e-hpa
# or
go test -v ./test/e2e/hpa/ -timeout 60m
```
