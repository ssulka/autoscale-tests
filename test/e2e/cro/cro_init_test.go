package cro

import (
	"testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCroInit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CRO Init Test")
}
