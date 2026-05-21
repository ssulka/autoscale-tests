package hpa

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/autoscale-tests/pkg/framework"
)

const (
	titleUp                 = "Should scale from 1 pod to 3 pods and then from 3 pods to 5 pods"
	titleDown               = "Should scale from 5 pods to 3 pods and then from 3 pods to 1 pod"
	titleAverageUtilization = " using Average Utilization for aggregation"
	titleAverageValue       = " using Average Value for aggregation"

	cpuResource = corev1.ResourceCPU
	memResource = corev1.ResourceMemory

	scaleTimeout    = 15 * time.Minute
	stabilityWindow = 10 * time.Minute
)

var f *framework.Framework

var _ = BeforeSuite(func() {
	var err error
	f, err = framework.NewFramework()
	Expect(err).NotTo(HaveOccurred(), "Failed to create framework")
})

var _ = Describe("HPA (Horizontal Pod Autoscaler)", func() {

	Context("Prerequisites", func() {
		It("should have the metrics API available", func() {
			By("Checking metrics.k8s.io API (required by HPA for pod CPU/memory metrics)")
			available, err := f.MetricsAPIAvailable(f.Ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(available).To(BeTrue(),
				"metrics.k8s.io API must be available — ensure metrics-server is running")
			GinkgoWriter.Printf("[Test] metrics.k8s.io API is available\n")
		})
	})

	// CPU-based scaling (Pod Resource)

	Describe("CPU-based scaling — Deployment (Pod Resource)", func() {

		It(titleUp+titleAverageUtilization, func() {
			scaleUp(cpuResource, autoscalingv2.UtilizationMetricType, false)
		})

		It(titleDown+titleAverageUtilization, func() {
			scaleDown(cpuResource, autoscalingv2.UtilizationMetricType, false)
		})

		It(titleUp+titleAverageValue, func() {
			scaleUp(cpuResource, autoscalingv2.AverageValueMetricType, false)
		})
	})

	// CPU-based scaling (Container Resource)

	Describe("CPU-based scaling — Deployment (Container Resource)", func() {

		It(titleUp+titleAverageUtilization, func() {
			scaleUpContainerResource(cpuResource, autoscalingv2.UtilizationMetricType)
		})

		It(titleUp+titleAverageValue, func() {
			scaleUpContainerResource(cpuResource, autoscalingv2.AverageValueMetricType)
		})
	})

	// Memory-based scaling (Pod Resource)

	Describe("Memory-based scaling — Deployment (Pod Resource)", func() {

		It(titleUp+titleAverageUtilization, func() {
			scaleUp(memResource, autoscalingv2.UtilizationMetricType, false)
		})

		It(titleUp+titleAverageValue, func() {
			scaleUp(memResource, autoscalingv2.AverageValueMetricType, false)
		})
	})

	// Memory-based scaling (Container Resource)

	Describe("Memory-based scaling — Deployment (Container Resource)", func() {

		It(titleUp+titleAverageUtilization, func() {
			scaleUpContainerResource(memResource, autoscalingv2.UtilizationMetricType)
		})

		It(titleUp+titleAverageValue, func() {
			scaleUpContainerResource(memResource, autoscalingv2.AverageValueMetricType)
		})
	})

	// Light scaling (1→2, 2→1)

	Describe("Deployment light", func() {

		It("Should scale from 1 pod to 2 pods", func() {
			st := &HPAScaleTest{
				initPods:         1,
				initCPUTotal:     150,
				perPodCPURequest: 200,
				perPodMemRequest: 200,
				targetValue:      50,
				minPods:          1,
				maxPods:          2,
				firstScale:       2,
				resourceType:     cpuResource,
				metricTargetType: autoscalingv2.UtilizationMetricType,
			}
			st.run("hpa-light-up")
		})

		It("Should scale from 2 pods to 1 pod", func() {
			st := &HPAScaleTest{
				initPods:         2,
				initCPUTotal:     50,
				perPodCPURequest: 200,
				perPodMemRequest: 200,
				targetValue:      50,
				minPods:          1,
				maxPods:          2,
				firstScale:       1,
				resourceType:     cpuResource,
				metricTargetType: autoscalingv2.UtilizationMetricType,
			}
			st.run("hpa-light-down")
		})
	})

	// Sidecar tests (ContainerResource use case)
	
	Describe("Deployment with idle sidecar (ContainerResource use case)", func() {

		It(titleUp+" on a busy application with an idle sidecar container", func() {
			scaleOnIdleSidecar(cpuResource, autoscalingv2.UtilizationMetricType, false)
		})

		It("Should not scale up on a busy sidecar with an idle application", func() {
			doNotScaleOnBusySidecar(cpuResource, autoscalingv2.UtilizationMetricType)
		})
	})
})

// HPAScaleTest

type HPAScaleTest struct {
	initPods         int32
	initCPUTotal     int
	initMemTotal     int
	perPodCPURequest int64 // millicores
	perPodMemRequest int64 // megabytes
	targetValue      int32
	minPods          int32
	maxPods          int32
	firstScale       int32
	firstScaleStasis time.Duration
	cpuBurst         int
	memBurst         int
	secondScale      int32
	resourceType     corev1.ResourceName
	metricTargetType autoscalingv2.MetricTargetType
}

func (st *HPAScaleTest) run(name string) {
	var testNamespace string

	By(fmt.Sprintf("Creating test namespace for %q", name))
	var err error
	testNamespace, err = f.CreateTestNamespace(f.Ctx, name)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if testNamespace != "" {
			_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
		}
	})

	initCPU, initMem := 0, 0
	if st.resourceType == cpuResource {
		initCPU = st.initCPUTotal
	} else if st.resourceType == memResource {
		initMem = st.initMemTotal
	}

	By("Creating resource-consumer deployment + service")
	rc, err := f.CreateResourceConsumer(f.Ctx, framework.ResourceConsumerConfig{
		Name:         name,
		Namespace:    testNamespace,
		Replicas:     st.initPods,
		CPURequest:   st.perPodCPURequest,
		MemRequest:   st.perPodMemRequest,
		InitCPUTotal: initCPU,
		InitMemTotal: initMem,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(rc.CleanUp)
	GinkgoWriter.Printf("[Test] Resource consumer %q ready (replicas: %d, initCPU: %d, initMem: %d)\n",
		name, st.initPods, initCPU, initMem)

	By("Creating HPA")
	hpaCfg := buildResourceHPAConfig(name, testNamespace, name, st.resourceType, st.metricTargetType,
		st.targetValue, st.minPods, st.maxPods)
	hpa, err := f.CreateHPA(f.Ctx, hpaCfg)
	Expect(err).NotTo(HaveOccurred())
	GinkgoWriter.Printf("[Test] HPA %q created (min=%d, max=%d)\n", hpa.Name, st.minPods, st.maxPods)

	scaleUp := st.firstScale > st.initPods
	if scaleUp {
		By(fmt.Sprintf("Waiting for scale UP to at least %d replicas", st.firstScale))
		err = f.WaitForHPAScaleAtLeast(f.Ctx, hpa.Name, testNamespace, st.firstScale, scaleTimeout)
	} else {
		By(fmt.Sprintf("Waiting for scale DOWN to at most %d replicas", st.firstScale))
		err = f.WaitForHPAScaleAtMost(f.Ctx, hpa.Name, testNamespace, st.firstScale, scaleTimeout)
	}
	Expect(err).NotTo(HaveOccurred(), "HPA should have scaled to %d replicas", st.firstScale)
	currentHPA, _ := f.GetHPA(f.Ctx, hpa.Name, testNamespace)
	GinkgoWriter.Printf("[Test] HPA at %d replicas\n", currentHPA.Status.CurrentReplicas)

	if st.firstScaleStasis > 0 {
		By(fmt.Sprintf("Verifying decision stability for %v", st.firstScaleStasis))
		err = f.EnsureHPAReplicasInRange(f.Ctx, hpa.Name, testNamespace,
			st.firstScale, st.firstScale+1, st.firstScaleStasis)
		Expect(err).NotTo(HaveOccurred(), "HPA should remain stable")
	}

	if st.resourceType == cpuResource && st.cpuBurst > 0 && st.secondScale > 0 {
		By(fmt.Sprintf("Bursting CPU to %d millicores, expecting scale to %d", st.cpuBurst, st.secondScale))
		rc.ConsumeCPU(st.cpuBurst)
		if st.secondScale > st.firstScale {
			err = f.WaitForHPAScaleAtLeast(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		} else {
			err = f.WaitForHPAScaleAtMost(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		}
		Expect(err).NotTo(HaveOccurred(), "HPA should have scaled to %d after CPU burst", st.secondScale)
		currentHPA, _ = f.GetHPA(f.Ctx, hpa.Name, testNamespace)
		GinkgoWriter.Printf("[Test] HPA at %d replicas (after burst)\n", currentHPA.Status.CurrentReplicas)
	}
	if st.resourceType == memResource && st.memBurst > 0 && st.secondScale > 0 {
		By(fmt.Sprintf("Bursting memory to %d MB, expecting scale to %d", st.memBurst, st.secondScale))
		rc.ConsumeMem(st.memBurst)
		if st.secondScale > st.firstScale {
			err = f.WaitForHPAScaleAtLeast(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		} else {
			err = f.WaitForHPAScaleAtMost(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		}
		Expect(err).NotTo(HaveOccurred(), "HPA should have scaled to %d after mem burst", st.secondScale)
		currentHPA, _ = f.GetHPA(f.Ctx, hpa.Name, testNamespace)
		GinkgoWriter.Printf("[Test] HPA at %d replicas (after burst)\n", currentHPA.Status.CurrentReplicas)
	}
}

// HPAContainerResourceScaleTest

type HPAContainerResourceScaleTest struct {
	initPods               int32
	initCPUTotal           int
	initMemTotal           int
	perContainerCPURequest int64
	perContainerMemRequest int64
	targetValue            int32
	minPods                int32
	maxPods                int32
	noScale                bool
	noScaleStasis          time.Duration
	firstScale             int32
	firstScaleStasis       time.Duration
	cpuBurst               int
	memBurst               int
	secondScale            int32
	sidecar                framework.SidecarMode
	resourceType           corev1.ResourceName
	metricTargetType       autoscalingv2.MetricTargetType
}

func (st *HPAContainerResourceScaleTest) run(name string) {
	var testNamespace string

	By(fmt.Sprintf("Creating test namespace for %q", name))
	var err error
	testNamespace, err = f.CreateTestNamespace(f.Ctx, name)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if testNamespace != "" {
			_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
		}
	})

	initCPU, initMem := 0, 0
	if st.resourceType == cpuResource {
		initCPU = st.initCPUTotal
	} else if st.resourceType == memResource {
		initMem = st.initMemTotal
	}

	By("Creating resource-consumer with sidecar")
	rc, err := f.CreateResourceConsumer(f.Ctx, framework.ResourceConsumerConfig{
		Name:         name,
		Namespace:    testNamespace,
		Replicas:     st.initPods,
		CPURequest:   st.perContainerCPURequest,
		MemRequest:   st.perContainerMemRequest,
		InitCPUTotal: initCPU,
		InitMemTotal: initMem,
		Sidecar:      st.sidecar,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(rc.CleanUp)

	By("Creating ContainerResource HPA")
	hpaCfg := buildContainerResourceHPAConfig(name, testNamespace, name,
		st.resourceType, st.metricTargetType, st.targetValue, st.minPods, st.maxPods)
	hpa, err := f.CreateHPA(f.Ctx, hpaCfg)
	Expect(err).NotTo(HaveOccurred())
	GinkgoWriter.Printf("[Test] HPA %q created (ContainerResource, min=%d, max=%d)\n",
		hpa.Name, st.minPods, st.maxPods)

	if st.noScale {
		if st.noScaleStasis > 0 {
			By(fmt.Sprintf("Verifying HPA does NOT scale for %v", st.noScaleStasis))
			err = f.EnsureHPAReplicasInRange(f.Ctx, hpa.Name, testNamespace,
				st.initPods, st.initPods, st.noScaleStasis)
			Expect(err).NotTo(HaveOccurred(), "HPA should not have scaled")
			GinkgoWriter.Printf("[Test] Confirmed: HPA did not scale\n")
		}
		return
	}

	scaleUp := st.firstScale > st.initPods
	if scaleUp {
		By(fmt.Sprintf("Waiting for scale UP to at least %d replicas", st.firstScale))
		err = f.WaitForHPAScaleAtLeast(f.Ctx, hpa.Name, testNamespace, st.firstScale, scaleTimeout)
	} else {
		By(fmt.Sprintf("Waiting for scale DOWN to at most %d replicas", st.firstScale))
		err = f.WaitForHPAScaleAtMost(f.Ctx, hpa.Name, testNamespace, st.firstScale, scaleTimeout)
	}
	Expect(err).NotTo(HaveOccurred(), "HPA should have scaled to %d replicas", st.firstScale)
	currentHPA, _ := f.GetHPA(f.Ctx, hpa.Name, testNamespace)
	GinkgoWriter.Printf("[Test] HPA at %d replicas\n", currentHPA.Status.CurrentReplicas)

	if st.firstScaleStasis > 0 {
		By(fmt.Sprintf("Verifying decision stability for %v", st.firstScaleStasis))
		err = f.EnsureHPAReplicasInRange(f.Ctx, hpa.Name, testNamespace,
			st.firstScale, st.firstScale+1, st.firstScaleStasis)
		Expect(err).NotTo(HaveOccurred())
	}

	if st.resourceType == cpuResource && st.cpuBurst > 0 && st.secondScale > 0 {
		By(fmt.Sprintf("Bursting CPU to %d, expecting scale to %d", st.cpuBurst, st.secondScale))
		rc.ConsumeCPU(st.cpuBurst)
		if st.secondScale > st.firstScale {
			err = f.WaitForHPAScaleAtLeast(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		} else {
			err = f.WaitForHPAScaleAtMost(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		}
		Expect(err).NotTo(HaveOccurred())
		currentHPA, _ = f.GetHPA(f.Ctx, hpa.Name, testNamespace)
		GinkgoWriter.Printf("[Test] HPA at %d replicas (after burst)\n", currentHPA.Status.CurrentReplicas)
	}
	if st.resourceType == memResource && st.memBurst > 0 && st.secondScale > 0 {
		By(fmt.Sprintf("Bursting memory to %d MB, expecting scale to %d", st.memBurst, st.secondScale))
		rc.ConsumeMem(st.memBurst)
		if st.secondScale > st.firstScale {
			err = f.WaitForHPAScaleAtLeast(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		} else {
			err = f.WaitForHPAScaleAtMost(f.Ctx, hpa.Name, testNamespace, st.secondScale, scaleTimeout)
		}
		Expect(err).NotTo(HaveOccurred())
		currentHPA, _ = f.GetHPA(f.Ctx, hpa.Name, testNamespace)
		GinkgoWriter.Printf("[Test] HPA at %d replicas (after burst)\n", currentHPA.Status.CurrentReplicas)
	}
}

// HPA config builders

func buildResourceHPAConfig(name, namespace, deploymentName string,
	resourceType corev1.ResourceName, metricTargetType autoscalingv2.MetricTargetType,
	targetValue, minReplicas, maxReplicas int32) framework.HPAConfig {

	cfg := framework.HPAConfig{
		Name:             name + "-hpa",
		Namespace:        namespace,
		TargetDeployment: deploymentName,
		MinReplicas:      minReplicas,
		MaxReplicas:      maxReplicas,
	}

	switch {
	case resourceType == cpuResource && metricTargetType == autoscalingv2.UtilizationMetricType:
		cfg.CPUTargetUtilization = &targetValue
	case resourceType == cpuResource && metricTargetType == autoscalingv2.AverageValueMetricType:
		cfg.CPUAverageValue = fmt.Sprintf("%dm", targetValue)
	case resourceType == memResource && metricTargetType == autoscalingv2.UtilizationMetricType:
		cfg.MemoryTargetUtilization = &targetValue
	case resourceType == memResource && metricTargetType == autoscalingv2.AverageValueMetricType:
		cfg.MemoryAverageValue = fmt.Sprintf("%dMi", targetValue)
	}

	return cfg
}

func buildContainerResourceHPAConfig(name, namespace, deploymentName string,
	resourceType corev1.ResourceName, metricTargetType autoscalingv2.MetricTargetType,
	targetValue, minReplicas, maxReplicas int32) framework.HPAConfig {

	cfg := framework.HPAConfig{
		Name:             name + "-hpa",
		Namespace:        namespace,
		TargetDeployment: deploymentName,
		MinReplicas:      minReplicas,
		MaxReplicas:      maxReplicas,
		ContainerName:    name,
	}

	switch {
	case resourceType == cpuResource && metricTargetType == autoscalingv2.UtilizationMetricType:
		cfg.ContainerCPUTargetUtilization = &targetValue
	case resourceType == cpuResource && metricTargetType == autoscalingv2.AverageValueMetricType:
		cfg.ContainerCPUAverageValue = fmt.Sprintf("%dm", targetValue)
	case resourceType == memResource && metricTargetType == autoscalingv2.UtilizationMetricType:
		cfg.ContainerMemoryTargetUtilization = &targetValue
	case resourceType == memResource && metricTargetType == autoscalingv2.AverageValueMetricType:
		cfg.ContainerMemoryAverageValue = fmt.Sprintf("%dMi", targetValue)
	}

	return cfg
}

// Scenario wrappers

func getTargetValue(avgValue, avgUtilization int32, targetType autoscalingv2.MetricTargetType) int32 {
	if targetType == autoscalingv2.UtilizationMetricType {
		return avgUtilization
	}
	return avgValue
}

func scaleUp(resourceType corev1.ResourceName, metricTargetType autoscalingv2.MetricTargetType, checkStability bool) {
	stasis := time.Duration(0)
	if checkStability {
		stasis = stabilityWindow
	}
	st := &HPAScaleTest{
		initPods:         1,
		perPodCPURequest: 500,
		perPodMemRequest: 500,
		targetValue:      getTargetValue(100, 20, metricTargetType),
		minPods:          1,
		maxPods:          5,
		firstScale:       3,
		firstScaleStasis: stasis,
		secondScale:      5,
		resourceType:     resourceType,
		metricTargetType: metricTargetType,
	}
	if resourceType == cpuResource {
		st.initCPUTotal = 250
		st.cpuBurst = 700
	}
	if resourceType == memResource {
		st.initMemTotal = 250
		st.memBurst = 700
	}
	st.run("hpa-up-" + string(resourceType))
}

func scaleDown(resourceType corev1.ResourceName, metricTargetType autoscalingv2.MetricTargetType, checkStability bool) {
	stasis := time.Duration(0)
	if checkStability {
		stasis = stabilityWindow
	}
	st := &HPAScaleTest{
		initPods:         5,
		perPodCPURequest: 500,
		perPodMemRequest: 500,
		targetValue:      getTargetValue(150, 30, metricTargetType),
		minPods:          1,
		maxPods:          5,
		firstScale:       3,
		firstScaleStasis: stasis,
		secondScale:      1,
		resourceType:     resourceType,
		metricTargetType: metricTargetType,
	}
	if resourceType == cpuResource {
		st.initCPUTotal = 325
		st.cpuBurst = 10
	}
	if resourceType == memResource {
		st.initMemTotal = 325
		st.memBurst = 10
	}
	st.run("hpa-down-" + string(resourceType))
}

func scaleUpContainerResource(resourceType corev1.ResourceName, metricTargetType autoscalingv2.MetricTargetType) {
	st := &HPAContainerResourceScaleTest{
		initPods:               1,
		perContainerCPURequest: 500,
		perContainerMemRequest: 500,
		targetValue:            getTargetValue(100, 20, metricTargetType),
		minPods:                1,
		maxPods:                5,
		firstScale:             3,
		secondScale:            5,
		sidecar:                framework.SidecarDisabled,
		resourceType:           resourceType,
		metricTargetType:       metricTargetType,
	}
	if resourceType == cpuResource {
		st.initCPUTotal = 250
		st.cpuBurst = 700
	}
	if resourceType == memResource {
		st.initMemTotal = 250
		st.memBurst = 700
	}
	st.run("hpa-container-up-" + string(resourceType))
}

func scaleOnIdleSidecar(resourceType corev1.ResourceName, metricTargetType autoscalingv2.MetricTargetType, checkStability bool) {
	stasis := time.Duration(0)
	if checkStability {
		stasis = stabilityWindow
	}
	st := &HPAContainerResourceScaleTest{
		initPods:               1,
		initCPUTotal:           125,
		perContainerCPURequest: 250,
		perContainerMemRequest: 250,
		targetValue:            20,
		minPods:                1,
		maxPods:                5,
		firstScale:             3,
		firstScaleStasis:       stasis,
		cpuBurst:               500,
		secondScale:            5,
		sidecar:                framework.SidecarIdle,
		resourceType:           resourceType,
		metricTargetType:       metricTargetType,
	}
	st.run("hpa-idle-sidecar")
}

func doNotScaleOnBusySidecar(resourceType corev1.ResourceName, metricTargetType autoscalingv2.MetricTargetType) {
	st := &HPAContainerResourceScaleTest{
		initPods:               1,
		initCPUTotal:           0,
		perContainerCPURequest: 500,
		perContainerMemRequest: 500,
		targetValue:            20,
		minPods:                1,
		maxPods:                5,
		sidecar:                framework.SidecarBusy,
		resourceType:           resourceType,
		metricTargetType:       metricTargetType,
		noScale:                true,
		noScaleStasis:          1 * time.Minute,
	}
	st.run("hpa-busy-sidecar")
}
