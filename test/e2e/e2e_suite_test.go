//go:build e2e
// +build e2e

/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/degoke/geass/test/utils"
)

func init() {
	if image := os.Getenv("E2E_MANAGER_IMAGE"); image != "" {
		managerImage = image
	}
}

var (
	// managerImage is the manager image to be built and loaded for testing.
	managerImage = "example.com/geass:v0.0.1"
	// shouldCleanupCertManager tracks whether CertManager was installed by this suite.
	shouldCleanupCertManager = false
)

// TestE2E runs the e2e test suite to validate the solution in an isolated environment.
// The default setup requires Kind. Cert-manager is not installed unless requested.
//
// To enable kubectl kuberc (use custom kubectl configurations), set: KUBECTL_KUBERC=true
// By default, kuberc is disabled to ensure consistent test behavior across different environments.
// To install CertManager for webhook tests, set: CERT_MANAGER_INSTALL=true
// To skip CertManager installation explicitly, set: CERT_MANAGER_INSTALL_SKIP=true
// To skip the image build (image must already exist locally), set: E2E_SKIP_DOCKER_BUILD=true
// To use the full multi-stage Dockerfile (pulls golang/distroless), set: E2E_DOCKER_BUILD=full
// To override the manager image tag, set: E2E_MANAGER_IMAGE=example.com/geass:v0.0.1
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting geass e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	if os.Getenv("E2E_SKIP_DOCKER_BUILD") != "true" {
		buildTarget := "docker-build-prebuilt"
		if os.Getenv("E2E_DOCKER_BUILD") == "full" {
			buildTarget = "docker-build"
		}

		By(fmt.Sprintf("building the manager image (%s)", buildTarget))
		cmd := exec.Command("make", buildTarget, fmt.Sprintf("IMG=%s", managerImage))
		_, err := utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager image")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping manager image build (E2E_SKIP_DOCKER_BUILD=true)\n")
	}

	// TODO(user): If you want to change the e2e test vendor from Kind,
	// ensure the image is built and available, then remove the following block.
	By("loading the manager image on Kind")
	err := utils.LoadImageToKindClusterWithName(managerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")

	configureKubectlKubeRC()
	setupCertManager()
})

var _ = AfterSuite(func() {
	teardownCertManager()
})

// Disable kubectl kuberc by default for test isolation.
// This prevents local kubectl configurations from affecting test behavior.
// To enable kuberc, set: KUBECTL_KUBERC=true
func configureKubectlKubeRC() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		By("disabling kubectl kuberc for test isolation")
		err := os.Setenv("KUBECTL_KUBERC", "false")
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to disable kubectl kuberc")
		_, _ = fmt.Fprintf(GinkgoWriter,
			"kubectl kuberc disabled for consistent test behavior (override with KUBECTL_KUBERC=true)\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "kubectl kuberc enabled (KUBECTL_KUBERC=true)\n")
	}
}

// setupCertManager installs CertManager when enabled for webhook tests.
// Skips installation by default; set CERT_MANAGER_INSTALL=true to opt in.
func setupCertManager() {
	if !certManagerInstallEnabled() {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager installation (set CERT_MANAGER_INSTALL=true to enable)\n")
		return
	}

	By("checking if CertManager is already installed")
	if utils.IsCertManagerCRDsInstalled() {
		_, _ = fmt.Fprintf(GinkgoWriter, "CertManager is already installed. Skipping installation.\n")
		return
	}

	// Mark for cleanup before installation to handle interruptions and partial installs.
	shouldCleanupCertManager = true

	By("installing CertManager")
	Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
}

func certManagerInstallEnabled() bool {
	if os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true" {
		return false
	}
	return os.Getenv("CERT_MANAGER_INSTALL") == "true"
}

// teardownCertManager uninstalls CertManager if it was installed by setupCertManager.
// This ensures we only remove what we installed.
func teardownCertManager() {
	if !shouldCleanupCertManager {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager cleanup (not installed by this suite)\n")
		return
	}

	By("uninstalling CertManager")
	utils.UninstallCertManager()
}
