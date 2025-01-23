
# Image URL to use all building/pushing image targets
IMG ?= milvusdb/milvus-operator:dev-latest
TOOL_IMG ?= milvus-config-tool:dev-latest
SIT_IMG ?= milvus-operator:sit
VERSION ?= 1.2.0
TOOL_VERSION ?= 1.0.0
MILVUS_HELM_VERSION ?= milvus-4.2.36
RELEASE_IMG ?= milvusdb/milvus-operator:v$(VERSION)
TOOL_RELEASE_IMG ?= milvusdb/milvus-config-tool:v$(TOOL_VERSION)
KIND_CLUSTER ?= kind

# Produce remove descriptions, it's too long
CRD_OPTIONS ?= "crd:maxDescLen=0"
# cert-manager
CERT_MANAGER_MANIFEST ?= "https://github.com/jetstack/cert-manager/releases/download/v1.5.3/cert-manager.yaml"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

CERT_DIR = ${TMPDIR}/k8s-webhook-server/serving-certs
CSR_CONF = config/cert/csr.conf
DEV_HOOK_PATCH  = config/dev/webhook_patch.yaml

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role  webhook paths="./..." output:crd:artifacts:config=config/crd/bases

generate: controller-gen go-generate ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

generate-all: generate deploy-manifests

go-generate:
	go install go.uber.org/mock/mockgen@v0.4.0
	go generate ./...

generate-client-groups:
	./hack/update-codegen.sh

fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	go vet ./...

ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
test: manifests generate fmt vet test-only ## Run tests.

code-check: go-generate fmt vet

test-only: 	## -race requires cgo
	CGO_ENABLED=1 go test -race ./... -coverprofile tmp.out; cat tmp.out | sed '/zz_generated.deepcopy.go/d' | sed '/_mock.go/d'  > cover.out

##@ Build
VERSION_PATH=github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1
BUILD_LDFLAGS=-X '$(VERSION_PATH).Version=$(VERSION)' -X '$(VERSION_PATH).MilvusHelmVersion=$(MILVUS_HELM_VERSION)'

build: generate fmt vet ## Build manager binary.
	go build -o bin/manager -ldflags="$(BUILD_LDFLAGS)" main.go

build-only:
	go build -o bin/manager -ldflags="$(BUILD_LDFLAGS)" main.go

build-config-tool:
	mkdir -p out
	CGO_ENABLED=0 go build -ldflags="-s -w" -o out/merge ./tool/merge
	CGO_ENABLED=0 go build -ldflags="-s -w" -o out/cp ./tool/cp
	CGO_ENABLED=0 go build -ldflags="-s -w" -o out/iam-verify ./tool/iam-verify

build-release: build-config-tool
	mkdir -p out
	CGO_ENABLED=0 go build -ldflags="$(BUILD_LDFLAGS)" -o out/manager main.go

run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go

docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} .

docker-push: ## Push docker image with the manager.
	docker push ${IMG}

out/config/assets/templates:
	mkdir -p out/config/assets
	cp -r config/assets/templates out/config/assets/templates

docker-prepare: build-release out/config/assets/templates
	mkdir -p ./out/config/assets/charts/
	wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/etcd-6.3.3.tgz -O ./etcd.tgz
	wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/minio-8.0.17.tgz -O ./minio.tgz
	wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/pulsarv2-2.7.8.tgz -O ./pulsar.tgz
	wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/pulsar-3.3.0.tgz -O ./pulsarv3.tgz
	wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/kafka-15.5.1.tgz -O ./kafka.tgz
	tar -xf ./etcd.tgz -C ./out/config/assets/charts/
	tar -xf ./minio.tgz -C ./out/config/assets/charts/
	tar -xf ./pulsarv3.tgz -C ./out/config/assets/
	mv ./out/config/assets/pulsar ./out/config/assets/charts/pulsarv3
	tar -xf ./pulsar.tgz -C ./out/config/assets/charts/
	tar -xf ./kafka.tgz -C ./out/config/assets/charts/
	wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/values.yaml -O ./out/config/assets/charts/values.yaml
	cp ./scripts/run.sh ./out/run.sh
	cp ./scripts/run-helm.sh ./out/run-helm.sh
	cp ./scripts/init.sh ./out/init.sh

docker-tool-prepare: build-config-tool
	mkdir -p out/tool
	cp ./scripts/run-helm.sh ./out/tool/run-helm.sh
	cp ./out/merge ./out/tool/merge
	cp ./out/cp ./out/tool/cp
	cp ./out/iam-verify ./out/tool/iam-verify

docker-tool-build:
	docker build --platform=linux/amd64 -t ${TOOL_RELEASE_IMG}-amd64 -f tool.Dockerfile .
	docker build --platform=linux/arm64 -t ${TOOL_RELEASE_IMG}-arm64 -f tool.Dockerfile .

docker-tool-push:
	docker manifest create ${TOOL_RELEASE_IMG} ${TOOL_RELEASE_IMG}-amd64 ${TOOL_RELEASE_IMG}-arm64
	docker manifest push ${TOOL_RELEASE_IMG}

docker-local-build:
	docker build -t ${IMG} -f local.Dockerfile .

docker-local: build-release docker-local-build

##@ Deployment

install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

deploy: ## Deploy controller to the K8s cluster specified in ~/.kube/config.
#	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
#	$(KUSTOMIZE) build config/default | kubectl apply -f -
	kubectl apply -f deploy/manifests/deployment.yaml

undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
#	$(KUSTOMIZE) build config/default | kubectl delete -f -
	kubectl delete -f deploy/manifests/deployment.yaml

deploy-dev: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/dev | kubectl apply -f -

deploy-cert-manager:
	kubectl apply -f ${CERT_MANAGER_MANIFEST}
	kubectl wait --timeout=3m --for=condition=Ready pods -l app.kubernetes.io/instance=cert-manager -n cert-manager

undeploy-cert-manager:
    kubectl delete -f ${CERT_MANAGER_MANIFEST}

deploy-manifests: manifests kustomize helm-generate
	# add namespace
	echo "---" > deploy/manifests/deployment.yaml
	cat config/manager/namespace.yaml >> deploy/manifests/deployment.yaml
	helm template milvus-operator --create-namespace -n milvus-operator ./charts/milvus-operator-$(VERSION).tgz  >> deploy/manifests/deployment.yaml

kind-dev: kind
	sudo $(KIND) create cluster --config config/kind/kind-dev.yaml --name ${KIND_CLUSTER}

uninstall-kind-dev: kind
	sudo $(KIND) delete cluster --name ${KIND_CLUSTER}

# Install local certificate
# Required for webhook server to start
dev-cert:
	$(RM) -r $(CERT_DIR)
	mkdir -p $(CERT_DIR)
	openssl genrsa -out $(CERT_DIR)/ca.key 2048
	openssl req -x509 -new -nodes -key $(CERT_DIR)/ca.key -subj "/CN=host.docker.internal" -days 10000 -out $(CERT_DIR)/ca.crt
	openssl genrsa -out $(CERT_DIR)/tls.key 2048
	openssl req -new -SHA256 -newkey rsa:2048 -nodes -keyout $(CERT_DIR)/tls.key -out $(CERT_DIR)/tls.csr -subj "/C=CN/ST=Shanghai/L=Shanghai/O=/OU=/CN=host.docker.internal"
	openssl req -new -key $(CERT_DIR)/tls.key -out $(CERT_DIR)/tls.csr -config $(CSR_CONF)
	openssl x509 -req -in $(CERT_DIR)/tls.csr -CA $(CERT_DIR)/ca.crt -CAkey $(CERT_DIR)/ca.key -CAcreateserial -out $(CERT_DIR)/tls.crt -days 10000 -extensions v3_ext -extfile $(CSR_CONF)
	
CA64=$(shell base64 -i $(CERT_DIR)/ca.crt)
CA=$(CA64:K==)
dev-cert-apply: dev-cert
	$(RM) -r config/dev/webhook_patch_ca.yaml
	echo '- op: "add"' > config/dev/webhook_patch_ca.yaml
	echo '  path: "/webhooks/0/clientConfig/caBundle"' >> config/dev/webhook_patch_ca.yaml
	echo "  value: $(CA)" >> config/dev/webhook_patch_ca.yaml

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.14.0)

KUSTOMIZE = $(shell pwd)/bin/kustomize
kustomize: ## Download kustomize locally if necessary.
	$(call go-get-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v3@v3.8.7)

KIND = $(shell pwd)/bin/kind
kind: ## Download kind locally if necessary.
	$(call go-get-tool,$(KIND),sigs.k8s.io/kind@v0.11.1)

##@ system integration test
sit-prepare-operator-images:
	@echo "Preparing operator images"
	docker build -t ${SIT_IMG} .
	docker pull -q quay.io/jetstack/cert-manager-controller:v1.5.3
	docker pull -q quay.io/jetstack/cert-manager-webhook:v1.5.3
	docker pull -q quay.io/jetstack/cert-manager-cainjector:v1.5.3

sit-prepare-images: sit-prepare-operator-images
	@echo "Preparing images"
	docker pull milvusdb/milvus:v2.5.4
	
	# docker pull -q apachepulsar/pulsar:2.8.2
	docker pull -q bitnami/kafka:3.1.0-debian-10-r52
	docker pull -q milvusdb/etcd:3.5.14-r1
	docker pull -q minio/minio:RELEASE.2023-03-20T20-16-18Z
	docker pull -q bitnami/pymilvus:2.4.6

sit-load-operator-images:
	@echo "Loading operator images"
	kind load docker-image ${SIT_IMG} --name ${KIND_CLUSTER}
	kind load docker-image quay.io/jetstack/cert-manager-controller:v1.5.3 --name ${KIND_CLUSTER}
	kind load docker-image quay.io/jetstack/cert-manager-webhook:v1.5.3 --name ${KIND_CLUSTER}
	kind load docker-image quay.io/jetstack/cert-manager-cainjector:v1.5.3 --name ${KIND_CLUSTER}

sit-load-images: sit-load-operator-images
	@echo "Loading images"
	kind load docker-image milvusdb/milvus:v2.5.4
	# kind load docker-image apachepulsar/pulsar:2.8.2 --name ${KIND_CLUSTER}
	kind load docker-image bitnami/kafka:3.1.0-debian-10-r52 --name ${KIND_CLUSTER}
	kind load docker-image milvusdb/etcd:3.5.14-r1 --name ${KIND_CLUSTER}
	kind load docker-image minio/minio:RELEASE.2023-03-20T20-16-18Z --name ${KIND_CLUSTER}
	kind load docker-image bitnami/pymilvus:2.4.6 --name ${KIND_CLUSTER}

sit-load-and-cleanup-images: sit-load-images
	@echo "Clean up some big images to save disk space in github action"
	docker rmi milvusdb/milvus:v2.5.4
	# docker rmi apachepulsar/pulsar:2.8.2
	docker rmi bitnami/kafka:3.1.0-debian-10-r52
	docker rmi milvusdb/etcd:3.5.14-r1
	docker rmi minio/minio:RELEASE.2023-03-20T20-16-18Z

sit-generate-manifest:
	cat deploy/manifests/deployment.yaml | sed  "s#${RELEASE_IMG}#${SIT_IMG}#g" > test/test_gen.yaml

sit-deploy: sit-load-and-cleanup-images
	@echo "Deploying"
	$(HELM) -n milvus-operator install --set image.repository=milvus-operator,image.tag=sit,resources.requests.cpu=10m --create-namespace milvus-operator ./charts/milvus-operator
	kubectl -n milvus-operator describe pods
	@echo "Waiting for operator to be ready"
	kubectl -n milvus-operator rollout restart deploy/milvus-operator
	kubectl -n milvus-operator wait --timeout=3m --for=condition=available deployments/milvus-operator
	sleep 5 #wait for the service to be ready

sit-test:
	./test/sit.sh ${test_mode}

cleanup-sit:
	kubectl delete -f test/test_gen.yaml

test-milvus-upgrade:
	./test/milvus-upgrade.sh
	
test-upgrade:
	./test/upgrade.sh

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go get $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

####################
#    Helm chart    #
####################

CHARTS_DIRECTORY      := charts
CHART_MILVUS_OPERATOR := $(CHARTS_DIRECTORY)/milvus-operator

CHART_REPO_URL := /milvus-operator/charts

DO_NOT_EDIT := Code generated by make. DO NOT EDIT.

# find helm or raise an error
.PHONY: helm
helm:
ifeq (, $(shell which helm 2> /dev/null))
	$(error Helm not found. Please install it: https://helm.sh/docs/intro/install/#from-script)
HELM=helm-not-found
else
HELM=$(shell which helm 2> /dev/null)
endif

.PHONY: helm-generate $(KUSTOMIZE) $(HELM)
helm-generate: $(CHARTS_DIRECTORY)/index.yaml

$(CHARTS_DIRECTORY)/index.yaml: $(CHARTS_DIRECTORY)/milvus-operator-$(VERSION).tgz
	$(HELM) repo index \
		--url $(CHART_REPO_URL) \
		$(CHARTS_DIRECTORY)

CHART_TEMPLATE_PATH := $(CHART_MILVUS_OPERATOR)/templates

$(CHARTS_DIRECTORY)/milvus-operator-$(VERSION).tgz: $(CHART_MILVUS_OPERATOR)/templates/crds.yaml \
	$(wildcard $(CHART_MILVUS_OPERATOR)/assets/*) \
	$(CHART_TEMPLATE_PATH)/role.yaml $(CHART_TEMPLATE_PATH)/clusterrole.yaml \
	$(CHART_TEMPLATE_PATH)/rolebinding.yaml $(CHART_TEMPLATE_PATH)/clusterrolebinding.yaml \
	$(CHART_TEMPLATE_PATH)/mutatingwebhookconfiguration.yaml $(CHART_TEMPLATE_PATH)/validatingwebhookconfiguration.yaml
	$(HELM) package $(CHART_MILVUS_OPERATOR) \
		--version $(VERSION) \
		--app-version $(VERSION) \
		--destination $(CHARTS_DIRECTORY)

$(CHART_MILVUS_OPERATOR)/templates/crds.yaml: kustomize config/crd/bases
	echo '{{- /* $(DO_NOT_EDIT) */ -}}' > '$@'
	echo '{{- if .Values.installCRDs }}' >> '$@'
	$(KUSTOMIZE) build config/helm/crds/ | \
	sed "s/'\({{[^}}]*}}\)'/\1/g">> '$@'
	echo '{{- end -}}' >> '$@'

$(CHART_TEMPLATE_PATH)/role.yaml: kustomize $(wildcard config/helm/rbac/*) $(wildcard config/rbac/*)
	echo '{{- /* $(DO_NOT_EDIT) */ -}}' > $(CHART_TEMPLATE_PATH)/role.yaml
	echo '{{- if .Values.rbac.create }}' >> $(CHART_TEMPLATE_PATH)/role.yaml
	$(KUSTOMIZE) build --reorder legacy config/helm/rbac | \
	$(KUSTOMIZE) cfg grep --annotate=false 'kind=Role' | \
	$(KUSTOMIZE) cfg grep --annotate=false --invert-match 'kind=ClusterRole' | \
	$(KUSTOMIZE) cfg grep --annotate=false --invert-match 'kind=RoleBinding' | \
	sed "s/'\({{[^}}]*}}\)'/\1/g" \
		>> $(CHART_TEMPLATE_PATH)/role.yaml
	echo '{{- end -}}' >> $(CHART_TEMPLATE_PATH)/role.yaml

$(CHART_TEMPLATE_PATH)/clusterrole.yaml: kustomize $(wildcard config/helm/rbac/*) $(wildcard config/rbac/*)
	echo '{{- /* $(DO_NOT_EDIT) */ -}}' > $(CHART_TEMPLATE_PATH)/clusterrole.yaml
	echo '{{- if .Values.rbac.create }}' >> $(CHART_TEMPLATE_PATH)/clusterrole.yaml
	$(KUSTOMIZE) build --reorder legacy config/helm/rbac | \
	$(KUSTOMIZE) cfg grep --annotate=false 'kind=ClusterRole' | \
	$(KUSTOMIZE) cfg grep --annotate=false --invert-match 'kind=ClusterRoleBinding' | \
	sed "s/'\({{[^}}]*}}\)'/\1/g" \
		>> $(CHART_TEMPLATE_PATH)/clusterrole.yaml
	echo '{{- end -}}' >> $(CHART_TEMPLATE_PATH)/clusterrole.yaml

$(CHART_TEMPLATE_PATH)/rolebinding.yaml: kustomize $(wildcard config/helm/rbac/*) $(wildcard config/rbac/*)
	echo '{{- /* $(DO_NOT_EDIT) */ -}}' > $(CHART_TEMPLATE_PATH)/rolebinding.yaml
	echo '{{- if .Values.rbac.create }}' >> $(CHART_TEMPLATE_PATH)/rolebinding.yaml
	$(KUSTOMIZE) build --reorder legacy config/helm/rbac | \
	$(KUSTOMIZE) cfg grep --annotate=false 'kind=RoleBinding' | \
	$(KUSTOMIZE) cfg grep --annotate=false --invert-match 'kind=ClusterRoleBinding' | \
	sed "s/'\({{[^}}]*}}\)'/\1/g" \
		>> $(CHART_TEMPLATE_PATH)/rolebinding.yaml
	echo '{{- end -}}' >> $(CHART_TEMPLATE_PATH)/rolebinding.yaml

$(CHART_TEMPLATE_PATH)/clusterrolebinding.yaml: kustomize $(wildcard config/helm/rbac/*) $(wildcard config/rbac/*)
	echo '{{- /* $(DO_NOT_EDIT) */ -}}' > $(CHART_TEMPLATE_PATH)/clusterrolebinding.yaml
	echo '{{- if .Values.rbac.create }}' >> $(CHART_TEMPLATE_PATH)/clusterrolebinding.yaml
	$(KUSTOMIZE) build --reorder legacy config/helm/rbac | \
	$(KUSTOMIZE) cfg grep --annotate=false 'kind=ClusterRoleBinding' | \
	sed "s/'\({{[^}}]*}}\)'/\1/g" \
		>> $(CHART_TEMPLATE_PATH)/clusterrolebinding.yaml
	echo '{{- end -}}' >> $(CHART_TEMPLATE_PATH)/clusterrolebinding.yaml

$(CHART_TEMPLATE_PATH)/validatingwebhookconfiguration.yaml: kustomize $(wildcard config/helm/webhook/*) $(wildcard config/webhook/*)
	echo '{{- /* $(DO_NOT_EDIT) */ -}}' > $(CHART_TEMPLATE_PATH)/validatingwebhookconfiguration.yaml
	echo '{{- if .Values.enableWebhook }}' >> $(CHART_TEMPLATE_PATH)/validatingwebhookconfiguration.yaml
	$(KUSTOMIZE) build --reorder legacy config/helm/webhook | \
	$(KUSTOMIZE) cfg grep --annotate=false 'kind=ValidatingWebhookConfiguration' | \
	sed "s/'\({{[^}}]*}}\)'/\1/g" \
		>> $(CHART_TEMPLATE_PATH)/validatingwebhookconfiguration.yaml
	echo '{{- end -}}' >> $(CHART_TEMPLATE_PATH)/validatingwebhookconfiguration.yaml

$(CHART_TEMPLATE_PATH)/mutatingwebhookconfiguration.yaml: kustomize $(wildcard config/helm/webhook/*) $(wildcard config/webhook/*)
	echo '{{- /* $(DO_NOT_EDIT) */ -}}' > $(CHART_TEMPLATE_PATH)/mutatingwebhookconfiguration.yaml
	echo '{{- if .Values.enableWebhook }}' >> $(CHART_TEMPLATE_PATH)/mutatingwebhookconfiguration.yaml
	$(KUSTOMIZE) build --reorder legacy config/helm/webhook | \
	$(KUSTOMIZE) cfg grep --annotate=false 'kind=MutatingWebhookConfiguration' | \
	sed "s/'\({{[^}}]*}}\)'/\1/g" \
		>> $(CHART_TEMPLATE_PATH)/mutatingwebhookconfiguration.yaml
	echo '{{- end -}}' >> $(CHART_TEMPLATE_PATH)/mutatingwebhookconfiguration.yaml

deploy-by-manifest: sit-prepare-operator-images sit-load-operator-images sit-generate-manifest
	@echo "Deploying Milvus Operator"
	kubectl apply -f ./test/test_gen.yaml
	@echo "Waiting for the operator to be ready..."
	kubectl -n milvus-operator rollout restart deploy/milvus-operator
	kubectl -n milvus-operator wait --timeout=3m --for=condition=available deployments/milvus-operator
	sleep 5 #wait for the service to be ready

debug-start: dev-cert
	kubectl -n milvus-operator patch deployment/milvus-operator --patch '{"spec":{"template":{"spec":{"containers":[{"name":"manager","args":["-namespace","milvus-operator","-name","milvus-operator","--health-probe-bind-address=:8081","--metrics-bind-address=:8080","--leader-elect","--stop-reconcilers=all"]}]}}}}'
	go run ./main.go -debug

debug-stop:
	kubectl -n milvus-operator patch deployment/milvus-operator --patch '{"spec":{"template":{"spec":{"containers":[{"name":"manager","args":["-namespace","milvus-operator","-name","milvus-operator","--health-probe-bind-address=:8081","--metrics-bind-address=:8080","--leader-elect"]}]}}}}'
