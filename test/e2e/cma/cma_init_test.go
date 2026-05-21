package cma

import (
	"testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCmaInit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CMA Init Test")
}
