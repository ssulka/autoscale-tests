package cma

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/autoscale-tests/pkg/framework"
)

var f *framework.Framework
var operatorInstalledByTest bool

var _ = BeforeSuite(func() {
	var err error
	f, err = framework.NewFramework()
	Expect(err).NotTo(HaveOccurred(), "Failed to create framework")

	By("Checking if CMA operator is already installed")
	installed, err := f.IsOperatorSubscribed(f.Ctx, "openshift-custom-metrics-autoscaler-operator", framework.CMANamespace)
	Expect(err).NotTo(HaveOccurred())
	GinkgoWriter.Printf("[BeforeSuite] CMA already installed: %v\n", installed)

	if !installed {
		operatorInstalledByTest = true
		By("Installing CMA operator from catalog")
		err = f.InstallOperatorByKey(f.Ctx, "cma")
		Expect(err).NotTo(HaveOccurred(), "Failed to install CMA operator")

		By("Waiting for CMA operator CSV to be ready")
		err = f.WaitForOperatorCSVReady(f.Ctx, framework.CMANamespace, 5*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "CMA operator CSV did not become ready")

		By("Waiting for CMA operator pods to be ready")
		err = f.WaitForOperatorReady(f.Ctx, "cma", 3*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "CMA operator pods did not become ready")
		GinkgoWriter.Printf("[BeforeSuite] CMA operator installed and ready\n")
	}
})

var _ = AfterSuite(func() {
	if f != nil && operatorInstalledByTest {
		By("Uninstalling CMA operator (installed by test)")
		GinkgoWriter.Printf("[AfterSuite] Uninstalling CMA operator...\n")
		err := f.UninstallOperatorByKey(f.Ctx, "cma")
		Expect(err).NotTo(HaveOccurred(), "Failed to uninstall CMA operator")
	} else {
		GinkgoWriter.Printf("[AfterSuite] Operator was pre-installed, skipping uninstall\n")
	}
})

var _ = Describe("Custom Metrics Autoscaler Operator", func() {

	Context("Installation verification", func() {

		It("should have the CMA namespace", func() {
			GinkgoWriter.Printf("[Test] Checking namespace: %s\n", framework.CMANamespace)
			exists, err := f.NamespaceExists(f.Ctx, framework.CMANamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "CMA namespace %s should exist", framework.CMANamespace)
		})

		It("should have the CMA operator pod running", func() {
			pods, err := f.GetOperatorPods(f.Ctx, "cma")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">", 0),
				"Should have at least one CMA operator pod in namespace %s", framework.CMANamespace)

			GinkgoWriter.Printf("[Test] CMA operator pods (%d):\n", len(pods.Items))
			for _, pod := range pods.Items {
				GinkgoWriter.Printf("  - %-50s Status: %s\n", pod.Name, pod.Status.Phase)
			}
		})

		It("should have all CMA operator pods in Ready state", func() {
			err := f.CheckOperatorHealth(f.Ctx, "cma")
			Expect(err).NotTo(HaveOccurred(), "All CMA operator pods should be healthy")
		})
	})

	Context("KEDA components verification", func() {

		It("should have keda-operator pod running and ready", func() {
			GinkgoWriter.Printf("[Test] Checking keda-operator pods in %s\n", framework.CMANamespace)
			err := f.WaitForOperatorReady(f.Ctx, "keda-operator", 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "keda-operator pod should be ready")

			pods, err := f.GetOperatorPods(f.Ctx, "keda-operator")
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("[Test] keda-operator pods (%d):\n", len(pods.Items))
			for _, pod := range pods.Items {
				GinkgoWriter.Printf("  - %-50s Status: %s\n", pod.Name, pod.Status.Phase)
			}
		})

		It("should have keda-metrics-apiserver pod running and ready", func() {
			GinkgoWriter.Printf("[Test] Checking keda-metrics-apiserver pods in %s\n", framework.CMANamespace)
			err := f.WaitForOperatorReady(f.Ctx, "keda-metrics-apiserver", 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "keda-metrics-apiserver pod should be ready")

			pods, err := f.GetOperatorPods(f.Ctx, "keda-metrics-apiserver")
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("[Test] keda-metrics-apiserver pods (%d):\n", len(pods.Items))
			for _, pod := range pods.Items {
				GinkgoWriter.Printf("  - %-50s Status: %s\n", pod.Name, pod.Status.Phase)
			}
		})

		It("should have keda-admission pod running and ready", func() {
			GinkgoWriter.Printf("[Test] Checking keda-admission pods in %s\n", framework.CMANamespace)
			err := f.WaitForOperatorReady(f.Ctx, "keda-admission", 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "keda-admission pod should be ready")

			pods, err := f.GetOperatorPods(f.Ctx, "keda-admission")
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("[Test] keda-admission pods (%d):\n", len(pods.Items))
			for _, pod := range pods.Items {
				GinkgoWriter.Printf("  - %-50s Status: %s\n", pod.Name, pod.Status.Phase)
			}
		})
	})

	Context("KEDA CRD verification", func() {

		It("should have ScaledObject CRD registered", func() {
			GinkgoWriter.Printf("[Test] Checking ScaledObject CRD (keda.sh)\n")
			exists, err := f.CRDExists(f.Ctx, "keda.sh", "ScaledObject")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "ScaledObject CRD should be registered by KEDA")
			GinkgoWriter.Printf("[Test] ScaledObject CRD exists: %v\n", exists)
		})

		It("should have ScaledJob CRD registered", func() {
			GinkgoWriter.Printf("[Test] Checking ScaledJob CRD (keda.sh)\n")
			exists, err := f.CRDExists(f.Ctx, "keda.sh", "ScaledJob")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "ScaledJob CRD should be registered by KEDA")
			GinkgoWriter.Printf("[Test] ScaledJob CRD exists: %v\n", exists)
		})

		It("should have TriggerAuthentication CRD registered", func() {
			GinkgoWriter.Printf("[Test] Checking TriggerAuthentication CRD (keda.sh)\n")
			exists, err := f.CRDExists(f.Ctx, "keda.sh", "TriggerAuthentication")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "TriggerAuthentication CRD should be registered by KEDA")
			GinkgoWriter.Printf("[Test] TriggerAuthentication CRD exists: %v\n", exists)
		})
	})

	Context("Cron scaler", func() {

		It("should scale out during a cron window and scale back in after", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "cma-cron")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "cma-cron-deploy"

			By("Creating a simple deployment with 1 replica")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.Replicas = 1
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			// Build cron window: starts 1 minute from now, ends 2 minutes from now.
			// This gives KEDA enough time to detect the trigger and scale out.
			now := time.Now().UTC()
			startMin := (now.Minute() + 1) % 60
			endMin := (startMin + 2) % 60

			By(fmt.Sprintf("Creating ScaledObject with cron trigger (start=%d, end=%d UTC minutes)", startMin, endMin))
			soName := "cma-cron-so"
			err = f.CreateScaledObject(f.Ctx, framework.ScaledObjectConfig{
				Name:           soName,
				Namespace:      testNamespace,
				DeploymentName: deploymentName,
				MinReplicas:    int64Ptr(1),
				MaxReplicas:    10,
				PollingInterval: int64Ptr(5),
				CooldownPeriod:  int64Ptr(5),
				ScaleDownStabilizationSeconds: int64Ptr(15),
				Triggers: []framework.ScaledObjectTrigger{{
					Type: "cron",
					Metadata: map[string]string{
						"timezone":        "Etc/UTC",
						"start":           fmt.Sprintf("%d * * * *", startMin),
						"end":             fmt.Sprintf("%d * * * *", endMin),
						"desiredReplicas": "4",
					},
				}},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteScaledObject(f.Ctx, soName, testNamespace)
			})

			By("Waiting for scale out to 4 replicas")
			err = f.WaitForKEDAScaleUp(f.Ctx, deploymentName, testNamespace, 4, 3*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Deployment should scale to 4 replicas during cron window")
			GinkgoWriter.Printf("[Test] Cron scale-out confirmed: deployment at >= 4 replicas\n")

			By("Waiting for scale in back to 1 replica after cron window ends")
			err = f.WaitForKEDAScaleDown(f.Ctx, deploymentName, testNamespace, 1, 5*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Deployment should scale back to 1 replica after cron window")
			GinkgoWriter.Printf("[Test] Cron scale-in confirmed: deployment at <= 1 replicas\n")
		})
	})

	// CPU Scaler — scale out on CPU utilization via ScaledObject
	Context("CPU scaler", func() {

		It("should scale out on high CPU utilization and scale back in when load stops", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "cma-cpu")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "cma-cpu-deploy"

			By("Creating resource-consumer deployment")
			rc, err := f.CreateResourceConsumer(f.Ctx, framework.ResourceConsumerConfig{
				Name:         deploymentName,
				Namespace:    testNamespace,
				Replicas:     1,
				CPURequest:   200,
				MemRequest:   64,
				InitCPUTotal: 0,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(rc.CleanUp)

			soName := "cma-cpu-so"
			By("Creating ScaledObject with CPU trigger (50% utilization)")
			err = f.CreateScaledObject(f.Ctx, framework.ScaledObjectConfig{
				Name:           soName,
				Namespace:      testNamespace,
				DeploymentName: deploymentName,
				MinReplicas:    int64Ptr(1),
				MaxReplicas:    5,
				ScaleDownStabilizationSeconds: int64Ptr(0),
				Triggers: []framework.ScaledObjectTrigger{{
					Type:       "cpu",
					MetricType: "Utilization",
					Metadata: map[string]string{
						"value": "50",
					},
				}},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteScaledObject(f.Ctx, soName, testNamespace)
			})

			By("Generating CPU load (500m across all pods, target is 50% of 200m = 100m)")
			rc.ConsumeCPU(500)

			By("Waiting for scale out to at least 2 replicas")
			err = f.WaitForKEDAScaleUp(f.Ctx, deploymentName, testNamespace, 2, 5*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Deployment should scale out under CPU load")
			dep, _ := f.GetDeployment(f.Ctx, deploymentName, testNamespace)
			GinkgoWriter.Printf("[Test] CPU scale-out confirmed: %d ready replicas\n", dep.Status.ReadyReplicas)

			By("Stopping CPU load")
			rc.ConsumeCPU(0)

			By("Waiting for scale in to 1 replica")
			err = f.WaitForKEDAScaleDown(f.Ctx, deploymentName, testNamespace, 1, 5*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Deployment should scale back to 1 after load stops")
			GinkgoWriter.Printf("[Test] CPU scale-in confirmed\n")
		})
	})

	// ScaledObject validation — KEDA admission webhook should reject invalid configs
	Context("ScaledObject validation", func() {

		It("should reject a second ScaledObject targeting the same deployment", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "cma-validate")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "cma-val-deploy"
			By("Creating a deployment")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			so1Name := "cma-val-so1"
			By("Creating first ScaledObject")
			err = f.CreateScaledObject(f.Ctx, framework.ScaledObjectConfig{
				Name:           so1Name,
				Namespace:      testNamespace,
				DeploymentName: deploymentName,
				MaxReplicas:    5,
				Triggers: []framework.ScaledObjectTrigger{{
					Type: "cron",
					Metadata: map[string]string{
						"timezone":        "Etc/UTC",
						"start":           "0 * * * *",
						"end":             "1 * * * *",
						"desiredReplicas": "1",
					},
				}},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = f.DeleteScaledObject(f.Ctx, so1Name, testNamespace)
			})

			so2Name := "cma-val-so2"
			By("Creating second ScaledObject targeting the same deployment — should be rejected")
			err = f.CreateScaledObject(f.Ctx, framework.ScaledObjectConfig{
				Name:           so2Name,
				Namespace:      testNamespace,
				DeploymentName: deploymentName,
				MaxReplicas:    5,
				Triggers: []framework.ScaledObjectTrigger{{
					Type: "cron",
					Metadata: map[string]string{
						"timezone":        "Etc/UTC",
						"start":           "0 * * * *",
						"end":             "1 * * * *",
						"desiredReplicas": "1",
					},
				}},
			})
			Expect(err).To(HaveOccurred(), "Second ScaledObject targeting same deployment should be rejected")
			GinkgoWriter.Printf("[Test] Correctly rejected duplicate ScaledObject: %v\n", err)
		})

		It("should reject a ScaledObject with CPU trigger when deployment has no CPU requests", func() {
			var testNamespace string
			By("Creating test namespace")
			var err error
			testNamespace, err = f.CreateTestNamespace(f.Ctx, "cma-val-cpu")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if testNamespace != "" {
					_ = f.CleanupTestNamespace(f.Ctx, testNamespace)
				}
			})

			deploymentName := "cma-nocpu-deploy"
			By("Creating a deployment without CPU requests")
			cfg := framework.DefaultDeploymentConfig(deploymentName, testNamespace)
			cfg.CPURequest = "0"
			cfg.CPULimit = "0"
			cfg.MemoryRequest = "64Mi"
			cfg.MemoryLimit = "128Mi"
			_, err = f.CreateDeployment(f.Ctx, cfg)
			Expect(err).NotTo(HaveOccurred())
			err = f.WaitForDeploymentReady(f.Ctx, deploymentName, testNamespace, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			soName := "cma-nocpu-so"
			By("Creating ScaledObject with CPU trigger — should be rejected")
			err = f.CreateScaledObject(f.Ctx, framework.ScaledObjectConfig{
				Name:           soName,
				Namespace:      testNamespace,
				DeploymentName: deploymentName,
				MaxReplicas:    5,
				Triggers: []framework.ScaledObjectTrigger{{
					Type:       "cpu",
					MetricType: "Utilization",
					Metadata: map[string]string{
						"value": "50",
					},
				}},
			})
			Expect(err).To(HaveOccurred(), "ScaledObject with CPU trigger should be rejected when deployment has no CPU requests")
			GinkgoWriter.Printf("[Test] Correctly rejected CPU ScaledObject without CPU requests: %v\n", err)
		})
	})
})

func int64Ptr(v int64) *int64 { return &v }
