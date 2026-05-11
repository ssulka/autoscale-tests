package cas

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCasInit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster Autoscaler Operator Test")
}
