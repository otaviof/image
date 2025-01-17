IMGCTRL = imgctrl
PLUGIN = kubectl-image
PLUGIN_DARWIN = kubectl-image-darwin

VERSION ?= v0.0.0
IMAGE_BUILDER ?= podman
IMAGE ?= quay.io/shipwright/imgctrl:latest

OUTPUT_DIR ?= output
OUTPUT_BIN = $(OUTPUT_DIR)/bin
OUTPUT_DOC = $(OUTPUT_DIR)/doc

IMGCTRL_BIN = $(OUTPUT_BIN)/$(IMGCTRL)
PLUGIN_BIN = $(OUTPUT_BIN)/$(PLUGIN)
GEN_BIN = $(OUTPUT_DIR)/code-generator
KUTTL_BIN = $(OUTPUT_DIR)/kuttl
KUTTL_REPO = https://github.com/kudobuilder/kuttl

PROJECT = github.com/shipwright-io/image
GEN_OUTPUT = /tmp/$(PROJECT)/infra/images

default: build

build: $(IMGCTRL) $(PLUGIN_DARWIN) $(PLUGIN)

.PHONY: $(IMGCTRL)
$(IMGCTRL):
	go build \
		-ldflags="-X 'main.Version=$(VERSION)'" \
		-o $(IMGCTRL_BIN) \
		./cmd/$(IMGCTRL)

.PHONY: $(PLUGIN)
$(PLUGIN):
	go build \
		-ldflags="-X 'main.Version=$(VERSION)'" \
		-o $(PLUGIN_BIN) \
		./cmd/$(PLUGIN)

.PHONY: $(PLUGIN_DARWIN)
$(PLUGIN_DARWIN):
	GOOS=darwin GOARCH=amd64 go build \
		-tags containers_image_openpgp \
		-ldflags="-X 'main.Version=$(VERSION)'" \
		-o $(PLUGIN_BIN) \
		./cmd/$(PLUGIN)

.PHONY: get-code-generator
get-code-generator:
	rm -rf $(GEN_BIN) || true
	git clone --depth=1 \
		--branch v0.22.0 \
		https://github.com/kubernetes/code-generator.git \
		$(GEN_BIN)

.PHONY: get-kuttl
get-kuttl:
	rm -rf $(KUTTL_BIN) || true
	mkdir -p $(OUTPUT_DIR) || true
	curl -o $(KUTTL_BIN) -L \
		$(KUTTL_REPO)/releases/download/v0.11.1/kubectl-kuttl_0.11.1_linux_x86_64
	chmod 755 $(KUTTL_BIN)

.PHONY: e2e
e2e:
	$(KUTTL_BIN) test e2e

.PHONY: generate-proto
generate-proto:
	protoc --go-grpc_out=paths=source_relative:. \
		--go_out=paths=source_relative:. \
		./infra/pb/*.proto

.PHONY: generate-k8s
generate-k8s:
	set -x
	rm -rf $(GEN_OUTPUT) || true
	$(GEN_BIN)/generate-groups.sh all \
		$(PROJECT)/infra/images/v1beta1/gen \
		$(PROJECT) \
		infra/images:v1beta1 \
		--go-header-file=$(GEN_BIN)/hack/boilerplate.go.txt \
		--output-base=/tmp
	rm -rf infra/images/v1beta1/gen
	mv $(GEN_OUTPUT)/v1beta1/* infra/images/v1beta1/

.PHONY: image
image:
	VERSION=$(VERSION) $(IMAGE_BUILDER) build -f Containerfile -t $(IMAGE) .

.PHONY: clean
clean:
	rm -rf $(OUTPUT_DIR)

.PHONY: pdf
pdf:
	mkdir -p $(OUTPUT_DOC)
	cat README.md | pandoc \
		-fmarkdown-implicit_figures \
		-V geometry:margin=1in \
		-o $(OUTPUT_DOC)/README.pdf
