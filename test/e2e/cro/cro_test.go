package cro

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

	By("Checking if CRO operator is already installed")
	installed, err := f.IsOperatorSubscribed(f.Ctx, "clusterresourceoverride", framework.CRONamespace)
	Expect(err).NotTo(HaveOccurred())
	GinkgoWriter.Printf("[BeforeSuite] CRO already installed: %v\n", installed)

	if !installed {
		operatorInstalledByTest = true
		By("Installing CRO operator from catalog")
		err = f.InstallOperatorByKey(f.Ctx, "cro")
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRO operator")

		By("Waiting for CRO operator CSV to be ready")
		err = f.WaitForOperatorCSVReady(f.Ctx, framework.CRONamespace, 5*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "CRO operator CSV did not become ready")

		By("Waiting for CRO operator pods to be ready")
		err = f.WaitForOperatorReady(f.Ctx, "cro", 3*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "CRO operator pods did not become ready")
		GinkgoWriter.Printf("[BeforeSuite] CRO operator installed and ready\n")
	}
})

var _ = AfterSuite(func() {
	if f != nil && operatorInstalledByTest {
		By("Uninstalling CRO operator (installed by test)")
		GinkgoWriter.Printf("[AfterSuite] Uninstalling CRO operator...\n")
		err := f.UninstallOperatorByKey(f.Ctx, "cro")
		Expect(err).NotTo(HaveOccurred(), "Failed to uninstall CRO operator")
	} else {
		GinkgoWriter.Printf("[AfterSuite] Operator was pre-installed, skipping uninstall\n")
	}
})

var _ = Describe("Cluster Resource Override Operator", func() {

	Context("Installation verification", func() {

		It("should have the CRO namespace", func() {
			exists, err := f.NamespaceExists(f.Ctx, framework.CRONamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue(), "CRO namespace %s should exist", framework.CRONamespace)
		})

		It("should have running operator pods", func() {
			pods, err := f.GetOperatorPods(f.Ctx, "cro")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pods.Items)).To(BeNumerically(">", 0),
				"Should have at least one CRO operator pod in namespace %s", framework.CRONamespace)

			By("Listing found pods")
			for _, pod := range pods.Items {
				GinkgoWriter.Printf("  - Pod: %s, Status: %s\n", pod.Name, pod.Status.Phase)
			}
		})

		It("should have all pods in Ready state", func() {
			err := f.CheckOperatorHealth(f.Ctx, "cro")
			Expect(err).NotTo(HaveOccurred(), "All CRO operator pods should be healthy")
		})
	})
})
