//go:build e2e
// +build e2e

package e2e

import (
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/degoke/geass/test/utils"
)

var _ = Describe("Platform project lifecycle", Ordered, func() {
	It("creates all isolated project environments", func() {
		manifest := `apiVersion: geass.geass.dev/v1alpha1
kind: GeassProject
metadata:
  name: e2e-platform
  namespace: geass-system
spec:
  displayName: E2E Platform
`
		apply := exec.Command("kubectl", "apply", "-f", "-")
		apply.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(apply)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "geassproject", "e2e-platform", "-n", "geass-system", "--ignore-not-found"))
		})
		for _, environment := range []string{"dev", "staging", "production"} {
			Eventually(func() error {
				_, err := utils.Run(exec.Command("kubectl", "get", "namespace", "e2e-platform-"+environment))
				return err
			}).Should(Succeed())
		}
	})
})
