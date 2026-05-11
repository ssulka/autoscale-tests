package hpa

import (
	"testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHPA(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HPA E2E Test Suite")
}
