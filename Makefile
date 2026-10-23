# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./api/..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: stub-installer-assets
stub-installer-assets: ## Create placeholder embed assets for vet, lint, and test.
	mkdir -p pkg/installer/assets
	@touch $(INSTALLER_OPERATOR_TAR) $(INSTALLER_OPERATOR_MANIFESTS)

.PHONY: vet
vet: stub-installer-assets ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: k3s-up
k3s-up: ## Start a throwaway k3s in Docker with Geass's CRDs, for the k3s integration tests. Use: eval "$$(make -s k3s-up)"
	@hack/k3s-up.sh

.PHONY: k3s-down
k3s-down: ## Remove the k3s that k3s-up started.
	@hack/k3s-down.sh

.PHONY: test-k3s-serverless
test-k3s-serverless: ## Run the scale-to-zero and function schedule test against the k3s that k3s-up started (takes about 15 minutes the first time).
	K3S_INTEGRATION=true go test -count=1 -tags=k3sintegration ./test/k3sintegration/ -run TestServerlessFunctionsOnK3s -v -timeout 40m

.PHONY: test-k3s-burst
test-k3s-burst: ## Run the burstable service test (a service that reserves less than it may use, and a copy stopped for memory saying so) against the k3s that k3s-up started (about 2 minutes, a little more the first time).
	K3S_INTEGRATION=true go test -count=1 -tags=k3sintegration ./test/k3sintegration/ -run TestBurstableServicesOnK3s -v -timeout 25m

.PHONY: test-k3s-objectstorage
test-k3s-objectstorage: ## Run the cluster object storage test (SeaweedFS and Garage, network policy, isolation) against the k3s that k3s-up started (takes about 10 minutes the first time).
	K3S_INTEGRATION=true go test -count=1 -tags=k3sintegration ./test/k3sintegration/ -run TestObjectStorageOnK3s -v -timeout 60m

.PHONY: test-k3s-limits
test-k3s-limits: ## Run the project limits test (a project keeping its services away from the nodes, and per-environment quotas) against the k3s that k3s-up started. Add a second node to it first (see test/k3sintegration/README.md) so a remote node is probed too.
	K3S_INTEGRATION=true go test -count=1 -tags=k3sintegration ./test/k3sintegration/ -run TestProjectLimitsOnK3s -v -timeout 25m

.PHONY: test-k3s-nodepool
test-k3s-nodepool: ## Run the node pool test (an autoscaling pool adding a node for pods that do not fit and removing it, with a pod on it, when they are gone) against the k3s that k3s-up started. Needs Docker and the rancher/k3s image that k3s-up used (about 3 minutes).
	K3S_INTEGRATION=true go test -tags=k3sintegration ./test/k3sintegration/ -run TestNodePoolAutoscalingOnK3s -v -timeout 20m

.PHONY: test-k3s-alerting
test-k3s-alerting: ## Run the alerting test (problems to Prometheus rules to Alertmanager to Slack and mail stand-ins on the host) against the k3s that k3s-up started (about 20 minutes, a little more the first time).
	K3S_INTEGRATION=true go test -count=1 -tags=k3sintegration ./test/k3sintegration/ -run TestAlertingOnK3s -v -timeout 50m

.PHONY: test-k3s-encryption
test-k3s-encryption: ## Run the secrets encryption test (a service reads its secret, and the value is not in k3s's datastore) against the k3s that k3s-up started (about 3 minutes, a little more the first time).
	K3S_INTEGRATION=true go test -count=1 -tags=k3sintegration ./test/k3sintegration/ -run TestSecretsAreEncryptedAtRestOnK3s -v -timeout 25m

.PHONY: test-alert-rules
test-alert-rules: ## Check the Prometheus rules Geass makes with promtool, and run the unit tests of their logic (test/alertrules). Needs Docker.
	@dir=$$(mktemp -d) && chmod 755 $$dir && \
	GEASS_DUMP_RULES=$$dir/rules.yaml go test ./internal/controller/ -run TestDumpAlertRules -count=1 >/dev/null && \
	cp test/alertrules/rules_test.yaml $$dir/rules_test.yaml && \
	docker run --rm --entrypoint promtool -v $$dir:/work prom/prometheus:v2.55.1 check rules /work/rules.yaml && \
	docker run --rm --entrypoint promtool -v $$dir:/work prom/prometheus:v2.55.1 test rules /work/rules_test.yaml; \
	status=$$?; rm -rf $$dir; exit $$status

.PHONY: test-k3s-restore
test-k3s-restore: ## Lose a control plane's disk and rebuild the cluster from the copy in S3 that K3s sent before, with a stand-in bucket and the scoped key the documentation gives (about 15 minutes).
	hack/k3s-restore-test.sh

.PHONY: test-k3s-ha
test-k3s-ha: ## Run a three-server K3s in Docker, run the operator on it, and take down the server it runs on (about 10 minutes).
	hack/k3s-ha-test.sh

.PHONY: loadtest
loadtest: ## Measure what the operator costs as a cluster grows. Use: make loadtest LOADTEST_ARGS="-apps 250,500,1000" (see hack/loadtest/main.go).
	go run ./hack/loadtest $(LOADTEST_ARGS)

.PHONY: test-k3s-integration
test-k3s-integration: ## Run K3s HelmChart integration tests (requires K3S_INTEGRATION=true and kubeconfig).
	K3S_INTEGRATION=true go test -count=1 -tags=k3sintegration ./test/k3sintegration/...

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true
# CertManager is skipped by default; enable webhook e2e setup with:
# - CERT_MANAGER_INSTALL=true
KIND_CLUSTER ?= geass-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

##@ Upgrades

# A cluster that is updated keeps what it stored with the CRDs of the release it ran, so a CRD change that
# drops a field or tightens a bound loses or invalidates that data. crd-compat compares the generated CRDs with
# the release in hack/crd-baseline-ref (see pkg/crdcompat for the rules). It reads that release from git, so it
# needs the full history: a shallow clone does not have it.
.PHONY: crd-compat
crd-compat: manifests ## Fail when the CRDs would lose or invalidate data stored by the baseline release. CRD_BASE_REF=<ref> overrides it.
	go run ./hack/crdcompat $(if $(CRD_BASE_REF),--base-ref $(CRD_BASE_REF))

# Cutting a release moves the baseline to it, so the next release is held to what this one shipped.
.PHONY: crd-baseline
crd-baseline: ## Make a release the compatibility baseline: make crd-baseline REF=v0.2.0
	@test -n "$(REF)" || { echo "Usage: make crd-baseline REF=<release tag>"; exit 1; }
	@git rev-parse --verify --quiet "$(REF)^{commit}" >/dev/null || { echo "$(REF) is not a tag or commit in this clone"; exit 1; }
	echo "$(REF)" > hack/crd-baseline-ref

##@ Lint

.PHONY: lint
lint: golangci-lint stub-installer-assets ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ CI

.PHONY: ci
ci: go-mod-tidy ci-lint ci-test ci-build ci-build-geass ci-e2e ## Run all CI checks (requires Docker and Kind).

.PHONY: ci-local
ci-local: go-mod-tidy ci-lint ci-test ci-build ## Run CI checks without Docker or Kind.

.PHONY: go-mod-tidy
go-mod-tidy: ## Tidy go.mod and go.sum.
	go mod tidy

.PHONY: ci-lint
ci-lint: lint-config lint ## Run the CI lint job.

.PHONY: ci-test
ci-test: test ## Run the CI unit test job.

.PHONY: ci-build
ci-build: build ## Build the manager binary.

.PHONY: ci-build-geass
ci-build-geass: build-geass ## Build the geass installer (requires Docker).

.PHONY: ci-e2e
ci-e2e: test-e2e ## Run the CI e2e job (requires Docker and Kind).

##@ Build

.PHONY: dashboard-build
dashboard-build: ## Build the Vite/React dashboard bundle.
	cd dashboard-ui && pnpm install --frozen-lockfile && pnpm build

.PHONY: dashboard-test
dashboard-test: ## Run the dashboard's tests (vitest).
	cd dashboard-ui && pnpm install --frozen-lockfile && pnpm test

.PHONY: dashboard-lib
dashboard-lib: ## Build the dashboard as a library, dashboard-ui/dist-lib (see docs/development.md).
	cd dashboard-ui && pnpm install --frozen-lockfile && pnpm build:lib

.PHONY: build
build: dashboard-build manifests generate fmt vet ## Build manager binary.
	go build -ldflags "$(GEASS_LDFLAGS)" -o bin/manager cmd/main.go

.PHONY: run dashboard-backend dashboard-dev
run: manifests generate fmt vet install ## Run the controller and React dashboard from your host (refreshes CRDs on the current kube context).
	$(MAKE) -j2 dashboard-backend dashboard-dev

# GEASS_ALLOW_LOOPBACK lets Settings > Sign-in use an identity provider on this machine (Better Auth on localhost:3000, say).
# It is only set here, where the operator runs on your host and loopback is your machine; an operator in a cluster leaves it off.
dashboard-backend: ## Run the Geass backend for the dashboard (API :8085; health :8086).
	GEASS_ALLOW_LOOPBACK=true go run ./cmd/main.go --dashboard-bind-address=:8085 --health-probe-bind-address=:8086

dashboard-dev: ## Run the Vite/React dashboard in development mode.
	cd dashboard-ui && pnpm dev --host 127.0.0.1

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: build-linux
build-linux: dashboard-build ## Build manager binary for Linux containers.
	@mkdir -p .build
	CGO_ENABLED=0 GOOS=linux GOARCH=$(shell go env GOARCH) go build -ldflags "$(GEASS_LDFLAGS)" -o .build/manager cmd/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build --build-arg VERSION=$(GEASS_VERSION) -t ${IMG} .

.PHONY: docker-build-prebuilt
docker-build-prebuilt: build-linux ## Build docker image from a local Linux binary (no registry pull).
	$(CONTAINER_TOOL) build -f Dockerfile.prebuilt -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# GEASS_VERSION is the release being built. The release workflow sets it to the tag; otherwise it is whatever
# git describes. It is built into the installer and the operator (pkg/release) and names the operator image,
# so that an installer that is applied over a running cluster really changes its Deployment and the operator
# restarts: the same tag for every release would leave the old operator running under the new manifests.
GEASS_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GEASS_LDFLAGS ?= -X github.com/degoke/geass/pkg/release.Version=$(GEASS_VERSION)
OPERATOR_IMAGE ?= geass-operator:$(GEASS_VERSION)
INSTALLER_OPERATOR_TAR ?= pkg/installer/assets/geass-operator.tar
INSTALLER_OPERATOR_MANIFESTS ?= pkg/installer/assets/operator.yaml
GEASS_GOOS ?= linux
GEASS_GOARCH ?= amd64

.PHONY: require-docker
require-docker: ## Fail fast when the container tool daemon is unavailable.
	@$(CONTAINER_TOOL) info >/dev/null 2>&1 || { \
		echo "Docker is required but $(CONTAINER_TOOL) is not available or the daemon is not running."; \
		echo "Start Docker and retry, or run: make ci-local"; \
		exit 1; \
	}

.PHONY: prepare-installer-assets
prepare-installer-assets: require-docker manifests generate kustomize ## Build operator image and export embeddable install assets.
	mkdir -p pkg/installer/assets
	$(CONTAINER_TOOL) build --platform linux/$(GEASS_GOARCH) --build-arg VERSION=$(GEASS_VERSION) -t $(OPERATOR_IMAGE) .
	$(CONTAINER_TOOL) save $(OPERATOR_IMAGE) -o $(INSTALLER_OPERATOR_TAR)
	# The manifests must name the image saved above. The checked-in kustomization names a placeholder
	# (controller:latest) that no node has, so without this the operator stays in ImagePullBackOff and the
	# install never finishes. The placeholder is put back so the working tree is left as it was found.
	cd config/manager && "$(KUSTOMIZE)" edit set image geass-operator=$(OPERATOR_IMAGE)
	"$(KUSTOMIZE)" build config/default > $(INSTALLER_OPERATOR_MANIFESTS)
	cd config/manager && "$(KUSTOMIZE)" edit set image geass-operator=controller:latest

.PHONY: build-geass
build-geass: prepare-installer-assets ## Build geass installer with embedded operator image and manifests.
	GOOS=$(GEASS_GOOS) GOARCH=$(GEASS_GOARCH) go build -ldflags "$(GEASS_LDFLAGS)" -o bin/geass ./cmd/geass/

# The command line, its MCP server (geass mcp) and the installer are one release: the command line warns when it
# and the cluster it talks to are different releases, so they are never versioned apart.
CLI_VERSION ?= $(GEASS_VERSION)
CLI_GOOS ?= $(shell go env GOOS)
CLI_GOARCH ?= $(shell go env GOARCH)

.PHONY: build-cli
build-cli: ## Build the geass command line (cmd/geass-cli) for CLI_GOOS/CLI_GOARCH into bin/geass-cli-<os>-<arch>.
	CGO_ENABLED=0 GOOS=$(CLI_GOOS) GOARCH=$(CLI_GOARCH) go build -trimpath -ldflags "-s -w -X main.version=$(CLI_VERSION)" \
		-o bin/geass-cli-$(CLI_GOOS)-$(CLI_GOARCH)$(if $(filter windows,$(CLI_GOOS)),.exe,) ./cmd/geass-cli/

# Every system the command line is released for. install-cli.sh covers linux and darwin; windows is downloaded by hand.
CLI_TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
INSTALLER_ARCHES ?= amd64 arm64

.PHONY: build-cli-all
build-cli-all: ## Build the command line for every system a release ships (CLI_TARGETS).
	@for target in $(CLI_TARGETS); do \
		$(MAKE) --no-print-directory build-cli CLI_GOOS=$${target%/*} CLI_GOARCH=$${target#*/} || exit 1; \
	done

.PHONY: build-geass-all
build-geass-all: ## Build the installer for every architecture a release ships (INSTALLER_ARCHES) into bin/geass-linux-<arch> (needs Docker).
	@for arch in $(INSTALLER_ARCHES); do \
		$(MAKE) --no-print-directory build-geass GEASS_GOARCH=$$arch || exit 1; \
		mv bin/geass bin/geass-linux-$$arch || exit 1; \
	done

# The files a release publishes, named from what was asked to be built, so a leftover binary in bin/ is not checksummed
# or listed. install.sh and install-cli.sh download these names; SHA256SUMS lists all of them, and the install scripts
# check what they download against it.
RELEASE_FILES = $(addprefix geass-linux-,$(INSTALLER_ARCHES)) \
	$(foreach target,$(CLI_TARGETS),geass-cli-$(subst /,-,$(target))$(if $(findstring windows,$(target)),.exe)) \
	install.sh install-cli.sh

.PHONY: release-checksums
release-checksums: ## Write bin/SHA256SUMS for the release files in bin/, with the install scripts beside them.
	cp scripts/install.sh scripts/install-cli.sh bin/
	cd bin && sha256sum $(RELEASE_FILES) > SHA256SUMS
	@echo "Wrote bin/SHA256SUMS"

.PHONY: release-assets
release-assets: build-geass-all build-cli-all release-checksums ## Build everything a release publishes into bin/ (needs Docker). Set GEASS_VERSION=vX.Y.Z.
	@ls -1 $(addprefix bin/,$(RELEASE_FILES) SHA256SUMS)

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# The Dockerfile already builds on BUILDPLATFORM and cross-compiles, so it is used as it is.
	- $(CONTAINER_TOOL) buildx create --name geass-builder
	$(CONTAINER_TOOL) buildx use geass-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --build-arg VERSION=$(GEASS_VERSION) --tag ${IMG} .
	- $(CONTAINER_TOOL) buildx rm geass-builder

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image geass-operator=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image geass-operator=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.21.0

#ENVTEST_VERSION is the controller-runtime version to use for setup-envtest, derived from go.mod
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v")

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.12.2
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
