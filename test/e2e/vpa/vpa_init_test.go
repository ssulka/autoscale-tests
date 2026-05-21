package vpa

import (
	"testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVpaInit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VPA Init Test")
}
