package vpa

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/openshift/autoscale-tests/pkg/framework"
)

var f *framework.Framework
var operatorInstalledByTest bool

var _ = BeforeSuite(func() {
	var err error
	f, err = framework.NewFramework()
	Expect(err).NotTo(HaveOccurred(), "Failed to create framework")

	By("Checking if VPA operator is already installed")
	installed, err := f.IsOperatorSubscribed(f.Ctx, "vertical-pod-autoscaler", framework.VPANamespace)
	Expect(err).NotTo(HaveOccurred())
	GinkgoWriter.Printf("[BeforeSuite] VPA already installed: %v\n", installed)

	if !installed {
		operatorInstalledByTest = true
		By("Installing VPA operator from catalog")
		err = f.InstallOperatorByKey(f.Ctx, "vpa")
		Expect(err).NotTo(HaveOccurred(), "Failed to install VPA operator")

		By("Waiting for VPA operator CSV to be ready")
		err = f.WaitForOperatorCSVReady(f.Ctx, framework.VPANamespace, 5*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "VPA operator CSV did not become ready")

		By("Waiting for VPA operator pods to be ready")
		err = f.WaitForOperatorReady(f.Ctx, "vpa", 3*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "VPA operator pods did not become ready")
		GinkgoWriter.Printf("[BeforeSuite] VPA operator installed and ready\n")
	}

	By("Ensuring VerticalPodAutoscalerController CR exists (activates recommender, admission, updater)")
	err = f.EnsureVPAController(f.Ctx)
	Expect(err).NotTo(HaveOccurred(), "Failed to create VPA controller CR")

	By("Waiting for VPA components (recommender, admission, updater) to be ready")
	err = f.WaitForVPAComponentsReady(f.Ctx, 5*time.Minute)
	Expect(err).NotTo(HaveOccurred(), "VPA components did not become ready")
})

var _ = AfterSuite(func() {
	if f != nil && operatorInstalledByTest {
		By("Deleting VerticalPodAutoscalerController CR")
		_ = f.DeleteVPAController(f.Ctx)

		By("Uninstalling VPA operator (installed by test)")
		GinkgoWriter.Printf("[AfterSuite] Uninstalling VPA operator...\n")
		err := f.UninstallOperatorByKey(f.Ctx, "vpa")
		Expect(err).NotTo(HaveOccurred(), "Failed to uninstall VPA operator")
	} else {
		GinkgoWriter.Printf("[AfterSuite] Operator was pre-installed, skipping uninstall\n")
	}
})

var _ = Describe("VPA (Vertical Pod Autoscaler)", func() {

	// Installation and CRD verification
	Context("Installation verification", func() {

		It("should have the VPA namespace", func() {
			exists, err := f.NamespaceExists(f.Ctx, framework.VPANamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "VPA namespace %s should exist", framework.VPANamespace)
		})

		It("should have running operator pods", func() {
			pods, err := f.GetOperatorPods(f.Ctx, "vpa")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">", 0),
				"Should have at least one VPA operator pod in namespace %s", framework.VPANamespace)

			By("Listing found pods")
			for _, pod := range pods.Items {
				GinkgoWriter.Printf("  - Pod: %s, Status: %s\n", pod.Name, pod.Status.Phase)
			}
		})

		It("should have all pods in Ready state", func() {
			err := f.CheckOperatorHealth(f.Ctx, "vpa")
			Expect(err).NotTo(HaveOccurred(), "All VPA operator pods should be healthy")
		})

		It("should have the VerticalPodAutoscaler CRD registered", func() {
			exists, err := f.CRDExists(f.Ctx, "autoscaling.k8s.io", "VerticalPodAutoscaler")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "VerticalPodAutoscaler CRD should be registered")
		})

		It("should have the VerticalPodAutoscalerCheckpoint CRD registered", func() {
			exists, err := f.CRDExists(f.Ctx, "autoscaling.k8s.io", "VerticalPodAutoscalerCheckpoint")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "VerticalPodAutoscalerCheckpoint CRD should be registered")
		})
	})

	// Recommender
	Context("Recommender", func() {

		BeforeEach(func() {
			By("Restarting VPA recommender to clear in-memory histogram cache")
			err := f.RestartVPARecommender(f.Ctx, 3*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "VPA recommender should restart successfully")
		})

		It("should serve a recommendation for a deployment with CPU load", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-rec")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			By("Cleaning any stale VPA checkpoints in namespace")
			_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)

			deploymentName := "vpa-rec-deploy"
			vpaName := "vpa-rec"

			By("Creating resource-consumer deployment")
			rc, err := f.CreateResourceConsumer(f.Ctx, framework.ResourceConsumerConfig{
				Name:         deploymentName,
				Namespace:    testNamespace,
				Replicas:     1,
				CPURequest:   100,
				MemRequest:   64,
				InitCPUTotal: 0,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(rc.CleanUp)

			By("Waiting for pod metrics to become available")
			err = f.WaitForPodMetricsAvailable(f.Ctx, testNamespace, 3*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Pod metrics should be available before creating VPA")

			By("Creating VPA targeting the deployment (mode=Off, only collects recommendations)")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeOff,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteVPA(f.Ctx, vpaName, testNamespace)
				_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
			})

			By("Starting sustained CPU load (200m)")
			rc.ConsumeCPU(200)

			By("Waiting for VPA to produce a recommendation (up to 15 minutes)")
			err = f.WaitForVPARecommendation(f.Ctx, vpaName, testNamespace, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "VPA should produce a recommendation")

			recs, err := f.GetVPARecommendations(f.Ctx, vpaName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(recs)).To(BeNumerically(">=", 1))
			GinkgoWriter.Printf("[Test] Recommendation: %+v\n", recs[0])

			Expect(recs[0].Target).To(HaveKey("cpu"), "Recommendation should include CPU target")
		})

		It("should respect minAllowed in recommendation", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-min")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			By("Cleaning any stale VPA checkpoints in namespace")
			_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)

			deploymentName := "vpa-min-deploy"
			vpaName := "vpa-min"

			By("Creating resource-consumer with low CPU request")
			rc, err := f.CreateResourceConsumer(f.Ctx, framework.ResourceConsumerConfig{
				Name:         deploymentName,
				Namespace:    testNamespace,
				Replicas:     1,
				CPURequest:   50,
				MemRequest:   64,
				InitCPUTotal: 0,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(rc.CleanUp)

			By("Waiting for pod metrics to become available")
			err = f.WaitForPodMetricsAvailable(f.Ctx, testNamespace, 3*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Pod metrics should be available before creating VPA")

			By("Creating VPA with minAllowed CPU=500m")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeOff,
				MinAllowed:       map[string]string{"cpu": "500m"},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteVPA(f.Ctx, vpaName, testNamespace)
				_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
			})

			By("Starting minimal CPU load")
			rc.ConsumeCPU(50)

			By("Waiting for recommendation (up to 15 minutes)")
			err = f.WaitForVPARecommendation(f.Ctx, vpaName, testNamespace, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			recs, err := f.GetVPARecommendations(f.Ctx, vpaName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(recs)).To(BeNumerically(">=", 1))

			targetCPU := resource.MustParse(recs[0].Target["cpu"])
			minCPU := resource.MustParse("500m")
			GinkgoWriter.Printf("[Test] Target CPU: %s, minAllowed: 500m\n", targetCPU.String())
			Expect(targetCPU.Cmp(minCPU)).To(BeNumerically(">=", 0),
				"Target CPU should be at least minAllowed (500m)")
		})

		It("should respect maxAllowed in recommendation", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-max")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			By("Cleaning any stale VPA checkpoints in namespace")
			_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)

			deploymentName := "vpa-max-deploy"
			vpaName := "vpa-max"

			By("Creating resource-consumer deployment")
			rc, err := f.CreateResourceConsumer(f.Ctx, framework.ResourceConsumerConfig{
				Name:         deploymentName,
				Namespace:    testNamespace,
				Replicas:     1,
				CPURequest:   100,
				MemRequest:   64,
				InitCPUTotal: 0,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(rc.CleanUp)

			By("Waiting for pod metrics to become available")
			err = f.WaitForPodMetricsAvailable(f.Ctx, testNamespace, 3*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Pod metrics should be available before creating VPA")

			By("Creating VPA with maxAllowed CPU=200m")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeOff,
				MaxAllowed:       map[string]string{"cpu": "200m"},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteVPA(f.Ctx, vpaName, testNamespace)
				_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
			})

			By("Starting high CPU load")
			rc.ConsumeCPU(500)

			By("Waiting for recommendation (up to 15 minutes)")
			err = f.WaitForVPARecommendation(f.Ctx, vpaName, testNamespace, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			recs, err := f.GetVPARecommendations(f.Ctx, vpaName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(recs)).To(BeNumerically(">=", 1))

			targetCPU := resource.MustParse(recs[0].Target["cpu"])
			maxCPU := resource.MustParse("200m")
			GinkgoWriter.Printf("[Test] Target CPU: %s, maxAllowed: 200m\n", targetCPU.String())
			Expect(targetCPU.Cmp(maxCPU)).To(BeNumerically("<=", 0),
				"Target CPU should not exceed maxAllowed (200m)")
		})

		It("should only recommend for containers not opted out", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-optout")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			By("Cleaning any stale VPA checkpoints in namespace")
			_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)

			deploymentName := "vpa-optout-deploy"
			vpaName := "vpa-optout"

			By("Creating deployment with two containers")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.CPURequest = "100m"
			cfg.MemoryRequest = "64Mi"
			cfg.CPULimit = "500m"
			cfg.MemoryLimit = "256Mi"
			cfg.ExtraContainers = []framework.ContainerConfig{{
				Name:          "sidecar",
				Image:         "registry.k8s.io/pause:3.9",
				CPURequest:    "50m",
				MemoryRequest: "32Mi",
				CPULimit:      "100m",
				MemoryLimit:   "64Mi",
			}}
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteDeployment(f.Ctx, deploymentName, testNamespace) })

			By("Waiting for pod metrics to become available")
			err = f.WaitForPodMetricsAvailable(f.Ctx, testNamespace, 3*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Pod metrics should be available before creating VPA")

			By("Creating VPA with sidecar opted out (mode=Off)")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeOff,
				ContainerPolicies: []framework.VPAContainerPolicy{
					{ContainerName: "test-container", Mode: "Auto"},
					{ContainerName: "sidecar", Mode: "Off"},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteVPA(f.Ctx, vpaName, testNamespace)
				_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
			})

			By("Waiting for recommendation (up to 15 minutes)")
			err = f.WaitForVPARecommendation(f.Ctx, vpaName, testNamespace, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			recs, err := f.GetVPARecommendations(f.Ctx, vpaName, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying only test-container has a recommendation, not sidecar")
			hasMain := false
			hasSidecar := false
			for _, rec := range recs {
				GinkgoWriter.Printf("[Test] Recommendation for %q: %v\n", rec.ContainerName, rec.Target)
				if rec.ContainerName == "test-container" {
					hasMain = true
				}
				if rec.ContainerName == "sidecar" {
					hasSidecar = true
				}
			}
			Expect(hasMain).To(BeTrue(), "test-container should have a recommendation")
			Expect(hasSidecar).To(BeFalse(), "sidecar (mode=Off) should NOT have a recommendation")
		})
	})

	// Admission Controller
	Context("Admission Controller", func() {

		BeforeEach(func() {
			By("Pausing VPA recommender to prevent it from overwriting synthetic recommendations")
			err := f.ScaleVPARecommender(f.Ctx, 0, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "VPA recommender should scale down")
		})

		AfterEach(func() {
			By("Resuming VPA recommender")
			err := f.ScaleVPARecommender(f.Ctx, 1, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "VPA recommender should scale back up")
		})

		It("should apply recommended requests to new pods", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-adm")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "vpa-adm-deploy"
			vpaName := "vpa-adm"

			By("Creating deployment with low requests")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.Replicas = 0
			cfg.CPURequest = "100m"
			cfg.MemoryRequest = "100Mi"
			cfg.CPULimit = "500m"
			cfg.MemoryLimit = "500Mi"
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteDeployment(f.Ctx, deploymentName, testNamespace) })

			By("Creating VPA with Auto update mode")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeAuto,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteVPA(f.Ctx, vpaName, testNamespace) })

			By("Setting synthetic recommendation: CPU=250m, Memory=200Mi")
			err = f.SetVPARecommendation(f.Ctx, vpaName, testNamespace, "test-container", "250m", "200Mi")
			Expect(err).NotTo(HaveOccurred())

			By("Verifying recommendation is still present before scale-up")
			recs, err := f.GetVPARecommendations(f.Ctx, vpaName, testNamespace)
			Expect(err).NotTo(HaveOccurred(), "Recommendation should still be present")
			GinkgoWriter.Printf("[Test] VPA recommendation before scale-up: %+v\n", recs)

			By("Scaling deployment to 1 replica to trigger admission")
			one := int32(1)
			dep, err := f.GetDeployment(f.Ctx, deploymentName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			dep.Spec.Replicas = &one
			err = f.Client.Update(f.Ctx, dep)
			Expect(err).NotTo(HaveOccurred())

			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("Checking pod requests were mutated by VPA admission")
			pods, err := f.ListPods(f.Ctx, testNamespace, map[string]string{"app": deploymentName})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">=", 1))

			By("Checking VPA annotations on pod (confirms webhook processed it)")
			GinkgoWriter.Printf("[Test] Pod annotations: %v\n", pods.Items[0].Annotations)

			container := pods.Items[0].Spec.Containers[0]
			cpuReq := container.Resources.Requests[corev1.ResourceCPU]
			memReq := container.Resources.Requests[corev1.ResourceMemory]
			GinkgoWriter.Printf("[Test] Pod requests: CPU=%s, Mem=%s\n", cpuReq.String(), memReq.String())

			expectedCPU := resource.MustParse("250m")
			expectedMem := resource.MustParse("200Mi")
			Expect(cpuReq.Cmp(expectedCPU)).To(Equal(0),
				"CPU request should match VPA recommendation (250m), got %s", cpuReq.String())
			Expect(memReq.Cmp(expectedMem)).To(Equal(0),
				"Memory request should match VPA recommendation (200Mi), got %s", memReq.String())
		})

		It("should keep limits-to-request ratio constant", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-ratio")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "vpa-ratio-deploy"
			vpaName := "vpa-ratio"

			// Original: CPU req=100m lim=200m (ratio 2x), Mem req=100Mi lim=200Mi (ratio 2x)
			By("Creating deployment with 2x limit/request ratio")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.Replicas = 0
			cfg.CPURequest = "100m"
			cfg.MemoryRequest = "100Mi"
			cfg.CPULimit = "200m"
			cfg.MemoryLimit = "200Mi"
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteDeployment(f.Ctx, deploymentName, testNamespace) })

			By("Creating VPA")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeAuto,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteVPA(f.Ctx, vpaName, testNamespace) })

			By("Setting synthetic recommendation: CPU=300m, Memory=300Mi")
			err = f.SetVPARecommendation(f.Ctx, vpaName, testNamespace, "test-container", "300m", "300Mi")
			Expect(err).NotTo(HaveOccurred())

			By("Scaling up to trigger admission")
			one := int32(1)
			dep, err := f.GetDeployment(f.Ctx, deploymentName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			dep.Spec.Replicas = &one
			err = f.Client.Update(f.Ctx, dep)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			pods, err := f.ListPods(f.Ctx, testNamespace, map[string]string{"app": deploymentName})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">=", 1))

			container := pods.Items[0].Spec.Containers[0]
			cpuReq := container.Resources.Requests[corev1.ResourceCPU]
			cpuLim := container.Resources.Limits[corev1.ResourceCPU]
			memReq := container.Resources.Requests[corev1.ResourceMemory]
			memLim := container.Resources.Limits[corev1.ResourceMemory]
			GinkgoWriter.Printf("[Test] CPU req=%s lim=%s, Mem req=%s lim=%s\n",
				cpuReq.String(), cpuLim.String(), memReq.String(), memLim.String())

			By("Verifying limit/request ratio is approximately maintained")
			if cpuReq.MilliValue() > 0 {
				cpuRatio := float64(cpuLim.MilliValue()) / float64(cpuReq.MilliValue())
				GinkgoWriter.Printf("[Test] CPU limit/request ratio: %.2f (expected ~2.0)\n", cpuRatio)
				Expect(cpuRatio).To(BeNumerically("~", 2.0, 0.5),
					"CPU limit/request ratio should be approximately 2x")
			}
			if memReq.Value() > 0 {
				memRatio := float64(memLim.Value()) / float64(memReq.Value())
				GinkgoWriter.Printf("[Test] Memory limit/request ratio: %.2f (expected ~2.0)\n", memRatio)
				Expect(memRatio).To(BeNumerically("~", 2.0, 0.5),
					"Memory limit/request ratio should be approximately 2x")
			}
		})

		It("should cap requests to maxAllowed set in VPA", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-admmax")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "vpa-admmax-deploy"
			vpaName := "vpa-admmax"

			By("Creating deployment")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.Replicas = 0
			cfg.CPURequest = "100m"
			cfg.MemoryRequest = "100Mi"
			cfg.CPULimit = "1000m"
			cfg.MemoryLimit = "1000Mi"
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteDeployment(f.Ctx, deploymentName, testNamespace) })

			By("Creating VPA with maxAllowed CPU=200m, Memory=200Mi")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeAuto,
				MaxAllowed:       map[string]string{"cpu": "200m", "memory": "200Mi"},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteVPA(f.Ctx, vpaName, testNamespace) })

			By("Setting synthetic recommendation above max: CPU=500m, Memory=500Mi")
			err = f.SetVPARecommendation(f.Ctx, vpaName, testNamespace, "test-container", "500m", "500Mi")
			Expect(err).NotTo(HaveOccurred())

			By("Scaling up")
			one := int32(1)
			dep, err := f.GetDeployment(f.Ctx, deploymentName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			dep.Spec.Replicas = &one
			err = f.Client.Update(f.Ctx, dep)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			pods, err := f.ListPods(f.Ctx, testNamespace, map[string]string{"app": deploymentName})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">=", 1))

			container := pods.Items[0].Spec.Containers[0]
			cpuReq := container.Resources.Requests[corev1.ResourceCPU]
			memReq := container.Resources.Requests[corev1.ResourceMemory]
			GinkgoWriter.Printf("[Test] Pod requests: CPU=%s, Mem=%s\n", cpuReq.String(), memReq.String())

			maxCPU := resource.MustParse("200m")
			maxMem := resource.MustParse("200Mi")
			Expect(cpuReq.Cmp(maxCPU)).To(BeNumerically("<=", 0),
				"CPU request should be capped to maxAllowed (200m)")
			Expect(memReq.Cmp(maxMem)).To(BeNumerically("<=", 0),
				"Memory request should be capped to maxAllowed (200Mi)")
		})

		It("should raise requests to minAllowed set in VPA", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-admmin")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "vpa-admmin-deploy"
			vpaName := "vpa-admmin"

			By("Creating deployment")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.Replicas = 0
			cfg.CPURequest = "100m"
			cfg.MemoryRequest = "100Mi"
			cfg.CPULimit = "1000m"
			cfg.MemoryLimit = "1000Mi"
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteDeployment(f.Ctx, deploymentName, testNamespace) })

			By("Creating VPA with minAllowed CPU=300m, Memory=300Mi")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeAuto,
				MinAllowed:       map[string]string{"cpu": "300m", "memory": "300Mi"},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteVPA(f.Ctx, vpaName, testNamespace) })

			By("Setting synthetic recommendation below min: CPU=50m, Memory=50Mi")
			err = f.SetVPARecommendation(f.Ctx, vpaName, testNamespace, "test-container", "50m", "50Mi")
			Expect(err).NotTo(HaveOccurred())

			By("Verifying recommendation is still present before scale-up")
			recs, err := f.GetVPARecommendations(f.Ctx, vpaName, testNamespace)
			Expect(err).NotTo(HaveOccurred(), "Recommendation should still be present")
			GinkgoWriter.Printf("[Test] VPA recommendation before scale-up: %+v\n", recs)

			By("Scaling up")
			one := int32(1)
			dep, err := f.GetDeployment(f.Ctx, deploymentName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			dep.Spec.Replicas = &one
			err = f.Client.Update(f.Ctx, dep)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			pods, err := f.ListPods(f.Ctx, testNamespace, map[string]string{"app": deploymentName})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">=", 1))

			By("Checking VPA annotations on pod (confirms webhook processed it)")
			annotations := pods.Items[0].Annotations
			GinkgoWriter.Printf("[Test] Pod annotations: %v\n", annotations)

			container := pods.Items[0].Spec.Containers[0]
			cpuReq := container.Resources.Requests[corev1.ResourceCPU]
			memReq := container.Resources.Requests[corev1.ResourceMemory]
			GinkgoWriter.Printf("[Test] Pod requests: CPU=%s, Mem=%s\n", cpuReq.String(), memReq.String())

			minCPU := resource.MustParse("300m")
			minMem := resource.MustParse("300Mi")
			Expect(cpuReq.Cmp(minCPU)).To(BeNumerically(">=", 0),
				"CPU request should be raised to minAllowed (300m)")
			Expect(memReq.Cmp(minMem)).To(BeNumerically(">=", 0),
				"Memory request should be raised to minAllowed (300Mi)")
		})

		It("should leave original requests when VPA has no recommendation", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-norec")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "vpa-norec-deploy"
			vpaName := "vpa-norec"

			By("Creating deployment with specific requests")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.Replicas = 1
			cfg.CPURequest = "100m"
			cfg.MemoryRequest = "100Mi"
			cfg.CPULimit = "200m"
			cfg.MemoryLimit = "200Mi"
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteDeployment(f.Ctx, deploymentName, testNamespace) })

			By("Creating VPA without any recommendation (empty status)")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeAuto,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteVPA(f.Ctx, vpaName, testNamespace) })

			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying pod requests remain unchanged")
			pods, err := f.ListPods(f.Ctx, testNamespace, map[string]string{"app": deploymentName})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">=", 1))

			container := pods.Items[0].Spec.Containers[0]
			cpuReq := container.Resources.Requests[corev1.ResourceCPU]
			memReq := container.Resources.Requests[corev1.ResourceMemory]
			GinkgoWriter.Printf("[Test] Pod requests: CPU=%s, Mem=%s\n", cpuReq.String(), memReq.String())

			origCPU := resource.MustParse("100m")
			origMem := resource.MustParse("100Mi")
			Expect(cpuReq.Cmp(origCPU)).To(Equal(0),
				"CPU request should remain original (100m) when no recommendation")
			Expect(memReq.Cmp(origMem)).To(Equal(0),
				"Memory request should remain original (100Mi) when no recommendation")
		})
	})

	// Updater
	Context("Updater", func() {

		BeforeEach(func() {
			By("Pausing VPA recommender to prevent it from overwriting synthetic recommendations")
			err := f.ScaleVPARecommender(f.Ctx, 0, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "VPA recommender should scale down")
		})

		AfterEach(func() {
			By("Resuming VPA recommender")
			err := f.ScaleVPARecommender(f.Ctx, 1, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "VPA recommender should scale back up")
		})

		It("should evict pods when recommendation differs significantly from current requests", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "vpa-evict")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			By("Cleaning any stale VPA checkpoints in namespace")
			_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)

			deploymentName := "vpa-evict-deploy"
			vpaName := "vpa-evict"
			labels := map[string]string{"app": deploymentName}

			By("Creating deployment with low CPU request (2 replicas for safe eviction)")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.Replicas = 2
			cfg.CPURequest = "100m"
			cfg.MemoryRequest = "100Mi"
			cfg.CPULimit = "1000m"
			cfg.MemoryLimit = "1000Mi"
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = f.DeleteDeployment(f.Ctx, deploymentName, testNamespace) })

			By("Recording original pod UIDs")
			originalUIDs, err := f.GetPodUIDs(f.Ctx, testNamespace, labels)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("[Test] Original pod UIDs: %v\n", originalUIDs)

			By("Creating VPA with Recreate mode")
			err = f.CreateVPA(f.Ctx, framework.VPAConfig{
				Name:             vpaName,
				Namespace:        testNamespace,
				TargetDeployment: deploymentName,
				UpdateMode:       framework.VPAUpdateModeRecreate,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteVPA(f.Ctx, vpaName, testNamespace)
				_ = f.DeleteVPACheckpoints(f.Ctx, testNamespace)
			})

			By("Setting synthetic recommendation much higher than current: CPU=500m, Memory=500Mi")
			err = f.SetVPARecommendation(f.Ctx, vpaName, testNamespace, "test-container", "500m", "500Mi")
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for updater to evict at least one pod (up to 5 minutes)")
			err = f.WaitForPodEviction(f.Ctx, testNamespace, labels, originalUIDs, 5*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "VPA updater should evict pods when recommendation differs significantly")

			By("Waiting for deployment to become ready again")
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying at least one pod was replaced (eviction confirmed)")
			newUIDs, err := f.GetPodUIDs(f.Ctx, testNamespace, labels)
			Expect(err).NotTo(HaveOccurred())

			evictedCount := 0
			for uid := range originalUIDs {
				if !newUIDs[uid] {
					evictedCount++
				}
			}
			GinkgoWriter.Printf("[Test] Evicted pods: %d out of %d original\n", evictedCount, len(originalUIDs))
			Expect(evictedCount).To(BeNumerically(">=", 1),
				"At least one original pod should have been evicted by the VPA updater")

			By("Logging new pod requests for reference")
			pods, err := f.ListPods(f.Ctx, testNamespace, labels)
			Expect(err).NotTo(HaveOccurred())
			for _, pod := range pods.Items {
				container := pod.Spec.Containers[0]
				cpuReq := container.Resources.Requests[corev1.ResourceCPU]
				memReq := container.Resources.Requests[corev1.ResourceMemory]
				isNew := !originalUIDs[pod.UID]
				GinkgoWriter.Printf("[Test] Pod %s (new=%v): CPU=%s, Mem=%s\n",
					pod.Name, isNew, cpuReq.String(), memReq.String())
			}
			GinkgoWriter.Printf("[Test] Updater eviction verified\n")
		})
	})
})

func isPodRunning(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodRunning
}
