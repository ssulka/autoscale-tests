package cma

import (
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
})
