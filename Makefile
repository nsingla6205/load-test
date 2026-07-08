imageVersion ?= latest
IMAGE_TAG_GOOGLE_PROXY_MIGRATE := vcp-db-migrate:${imageVersion}
IMAGE_TAG_IAM_LIFECYCLE := vcp-iam-lifecycle:${imageVersion}
IMAGE_TAG_GOOGLE_CLOUD_RUN:= vcp-cloudrun-deployer:${imageVersion}
IMAGE_TAG_VLM_WORKER_RECONCILER := vlm-worker-reconciler:${imageVersion}
GAR_REGISTRY ?= us-docker.pkg.dev/gcnv-artifact-registry-nonprod/vcp-container-images-us
GAR_IMAGE_TAG_VLM_WORKER_RECONCILER := $(GAR_REGISTRY)/vlm-worker-reconciler:${imageVersion}
GHVSA_PAT := ${GHVSA_PAT}

# Tool versions
MOCKERY_VERSION := v2.53.4

# Registry and timestamp configuration
DEV_REGISTRY ?= ghcr.io/vcp-vsa-control-plane
TIMESTAMP := $(shell date +%Y%m%d-%H%M%S)
IMAGE_TAG := $(TIMESTAMP)
WORKSPACE_MODULES := . ./cicd ./core ./database ./hyperscaler ./lib ./vcp-core

.PHONY: fix-imports
fix-imports:
	go get golang.org/x/tools/cmd/goimports
	goimports -local -format-only -w .

.PHONY: generate-mocks
generate-mocks:
	go install github.com/vektra/mockery/v2@$(MOCKERY_VERSION)
	mockery --config .mockery.yaml
	mockery --config .monkeyMocks.yaml

.PHONY: generate-monkey-mocks
generate-monkey-mocks:
	go install github.com/vektra/mockery/v2@$(MOCKERY_VERSION)
	mockery --config .monkeyMocks.yaml

.PHONY: link-check
link-check:
	go run ./scripts/link-checker/link-checker.go doc/

.PHONY: docs-check
docs-check:
	@echo "Checking documentation sync..."
	@echo "Running link check..."
	@make link-check
	@echo "Documentation check complete!"

.PHONY: docs-update
docs-update:
	@echo "Updating documentation..."
	@echo "Please review the following files for updates:"
	@echo "- doc/api/ (for API changes)"
	@echo "- doc/workflows/ (for workflow changes)"
	@echo "- doc/architecture/ (for architecture changes)"
	@echo "- doc/guides/ (for user-facing changes)"
	@echo "See doc/guides/documentation-updates.md for detailed instructions"

.PHONY: docs-validate
docs-validate:
	@echo "Validating documentation..."
	@make link-check
	@echo "Checking for missing documentation..."
	@find . -name "*.go" -path "./core/*" -exec grep -l "func.*Workflow\|func.*Activity" {} \; | while read file; do \
		basename=$$(basename $$file .go); \
		if [ ! -f "doc/workflows/core/$$basename.md" ] && [ ! -f "doc/workflows/background/$$basename.md" ]; then \
			echo "⚠️  Missing documentation for $$file"; \
		fi; \
	done
	@echo "Documentation validation complete!"

.PHONY: generate-cvp-client
generate-cvp-client:
	go install github.com/go-swagger/go-swagger/cmd/swagger@v0.25.0
	rm -rf clients/cvp/cvpapi clients/cvp/models
	cd clients/cvp;swagger generate client -f swagger-gcp.yaml -c cvpapi -A cvp

.PHONY: vcp-db-migrate-image
vcp-db-migrate-image: vcp-db-migrate-linux
	docker buildx build -t ${IMAGE_TAG_GOOGLE_PROXY_MIGRATE} --platform linux/amd64 -f vcp-core/migrate.Dockerfile .

.PHONY: vcp-db-migrate-linux
vcp-db-migrate-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o vcp-core/build/linux/bin/vcp-db-migrate ./tools/migrate

.PHONY: vcp-iam-lifecycle-image
vcp-iam-lifecycle-image: vcp-iam-lifecycle-linux
	docker buildx build -t ${IMAGE_TAG_IAM_LIFECYCLE} --platform linux/amd64 -f vcp-core/iam-lifecycle.Dockerfile .

.PHONY: vcp-iam-lifecycle-linux
vcp-iam-lifecycle-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o vcp-core/build/linux/bin/vcp-iam-lifecycle ./tools/iam-lifecycle

.PHONY: vcp-cloudrun-deployer-linux-image
vcp-cloudrun-deployer-linux-image: vcp-cloudrun-deployer-linux
	docker buildx build -t ${IMAGE_TAG_GOOGLE_CLOUD_RUN} --platform linux/amd64 -f vcp-core/cloud-run-deployer.Dockerfile .

.PHONY: vcp-cloudrun-deployer-linux
vcp-cloudrun-deployer-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o vcp-core/build/linux/bin/vcp-cloudrun-deployer ./tools/telemetry-deployer

.PHONY: vlm-worker-reconciler-image
vlm-worker-reconciler-image:
	docker buildx build -t ${IMAGE_TAG_VLM_WORKER_RECONCILER} --platform linux/amd64 -f tools/vlm-worker-reconciler/Dockerfile .

.PHONY: vlm-worker-reconciler-push
vlm-worker-reconciler-push: vlm-worker-reconciler-image
	docker tag ${IMAGE_TAG_VLM_WORKER_RECONCILER} ${GAR_IMAGE_TAG_VLM_WORKER_RECONCILER}
	docker push ${GAR_IMAGE_TAG_VLM_WORKER_RECONCILER}

.PHONY: generate-google-proxy
generate-google-proxy:
	go run github.com/ogen-go/ogen/cmd/ogen@v1.10.1 --clean --package gcpserver --config google-proxy/api/.ogenserver.yml --target google-proxy/api/gcp-servergen google-proxy/api/gcp-api.yaml
	go run github.com/ogen-go/ogen/cmd/ogen@v1.10.1 --clean --package googleproxyclient --config clients/google-proxy-client/.ogenserver.yml --target clients/google-proxy-client google-proxy/api/gcp-api.yaml

.PHONY: generate-ontap-proxy
generate-ontap-proxy:
	go run github.com/ogen-go/ogen/cmd/ogen@v1.10.1 --clean --package ontapserver --config ontap-proxy/api/.ogenserver.yml --target ontap-proxy/api/ontap-proxy-servergen ontap-proxy/api/api.yaml

.PHONY: generate-core-api
generate-core-api:
	go run github.com/ogen-go/ogen/cmd/ogen@v1.10.1 --clean --package coreapiserver --config vcp-core/.ogenserver.yml --target vcp-core/servergen vcp-core/api.yaml
	go run github.com/ogen-go/ogen/cmd/ogen@v1.10.1 --clean --package coreapi --config clients/core-api/.ogenclient.yml --target clients/core-api vcp-core/api.yaml
.PHONY: generate-metrics-api
generate-metrics-api:
	go run github.com/ogen-go/ogen/cmd/ogen@v1.10.1 --clean --package coreapiserver --config telemetry/api/.ogenserver.yml --target telemetry/api/telemetry-servergen telemetry/api/metrics-api.yaml

.PHONY: generate-retry-engine-wrapper
generate-retry-engine-wrapper:
	cd cmd/retry-engine-generator; go run main.go vcp database
	cd scripts; ./generate-retry-engine.sh vcp database
	cd cmd/retry-engine-generator; go run main.go metrics database/telemetry
	cd scripts; ./generate-retry-engine.sh metrics database/telemetry

.PHONY: generate-vcm-api
generate-vcm-api:
	go run github.com/ogen-go/ogen/cmd/ogen@v1.15.1 --clean --package vcmserver --config vcm-proxy/api/.ogenserver.yml --target vcm-proxy/api/vcm-servergen vcm-proxy/api/vcm-api.yaml

.PHONY: generate
generate: generate-google-proxy generate-core-api generate-metrics-api generate-retry-engine-wrapper generate-vcm-api

.PHONY: verify-generated
verify-generated: generate
verify-generated:
	cd scripts; ./verify-generated.sh


GOMODCACHE := $(shell go env GOMODCACHE)
GOCACHE := $(shell go env GOCACHE)

.PHONY: build-all-binaries-dev
build-all-binaries-dev:
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c '\
		go build -gcflags="all=-N -l" -o /src/app/vcp-worker ./worker/ && \
		go build -gcflags="all=-N -l" -o /src/app/google-proxy ./google-proxy/ && \
		go build -gcflags="all=-N -l" -o /src/app/oci-proxy ./oci-proxy/ && \
		go build -gcflags="all=-N -l" -o /src/app/core ./vcp-core/cmd && \
		go build -gcflags="all=-N -l" -o /src/app/telemetry ./telemetry/ && \
		go build -gcflags="all=-N -l" -o /src/app/vcm-proxy ./vcm-proxy/ && \
		go build -gcflags="all=-N -l" -o /src/app/ontap-proxy ./ontap-proxy/'

.PHONY: build-all-binaries-test
build-all-binaries-test:
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c '\
		go build -gcflags="all=-N -l" -buildvcs=false -o /src/app/vcp-worker ./worker/ && \
		go build -gcflags="all=-N -l" -buildvcs=false -o /src/app/google-proxy ./google-proxy/ && \
		go build -gcflags="all=-N -l" -buildvcs=false -o /src/app/oci-proxy ./oci-proxy/ && \
		go build -gcflags="all=-N -l" -buildvcs=false -o /src/app/core ./vcp-core/cmd && \
		go build -gcflags="all=-N -l" -buildvcs=false -o /src/app/telemetry ./telemetry/ && \
		go build -gcflags="all=-N -l" -buildvcs=false -o /src/app/vcm-proxy ./vcm-proxy/ && \
		go build -gcflags="all=-N -l" -buildvcs=false -o /src/app/ontap-proxy ./ontap-proxy/'

.PHONY: skaffold-dev
skaffold-dev:
	export $(cat skaffold.env | xargs)
	skaffold dev -p dev

.PHONY: skaffold-dev-oci
skaffold-dev-oci:
	export $(cat skaffold.env | xargs)
	@ctx="$$(kubectl config current-context 2>/dev/null)"; \
	if [ "$$ctx" != "docker-desktop" ]; then \
		echo "skaffold-dev-oci: refusing to run; current kubectl context is '$$ctx', expected 'docker-desktop'."; \
		echo "Switch with: kubectl config use-context docker-desktop"; \
		exit 1; \
	fi; \
	skaffold dev -p dev

.PHONY: build-all-binaries-prod
build-all-binaries-prod:
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all -t vsa-binaries-builder .
	mkdir -p artifacts
	docker rm -f vsa-binaries-builder-run || true
	docker run --name vsa-binaries-builder-run \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		vsa-binaries-builder sh -c '\
		go build -o /src/artifacts/vcp-worker ./worker/ && \
		go build -o /src/artifacts/google-proxy ./google-proxy/ && \
		go build -o /src/artifacts/oci-proxy ./oci-proxy/ && \
		go build -o /src/artifacts/core ./vcp-core/cmd && \
		go build -o /src/artifacts/telemetry ./telemetry/ && \
		go build -o /src/artifacts/vcm-proxy ./vcm-proxy/ && \
        go build -o /src/artifacts/ontap-proxy ./ontap-proxy/'
	docker cp vsa-binaries-builder-run:/src/artifacts/. ./artifacts/
	ls artifacts
	docker rm vsa-binaries-builder-run

.PHONY: build-all-binaries-prod-on-mac
build-all-binaries-prod-on-mac:
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all -t vsa-binaries-builder .
	mkdir -p artifacts
	docker rm -f vsa-binaries-builder-run || true
	docker run --name vsa-binaries-builder-run \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		vsa-binaries-builder sh -c '\
		GOOS=linux GOARCH=amd64  go build -o /src/artifacts/vcp-worker ./worker/ && \
		GOOS=linux GOARCH=amd64  go build -o /src/artifacts/google-proxy ./google-proxy/ && \
		GOOS=linux GOARCH=amd64  go build -o /src/artifacts/oci-proxy ./oci-proxy/ && \
		GOOS=linux GOARCH=amd64  go build -o /src/artifacts/core ./vcp-core/cmd && \
		GOOS=linux GOARCH=amd64  go build -o /src/artifacts/telemetry ./telemetry/ && \
		GOOS=linux GOARCH=amd64  go build -o /src/artifacts/vcm-proxy ./vcm-proxy/ && \
        GOOS=linux GOARCH=amd64  go build -o /src/artifacts/ontap-proxy ./ontap-proxy/'
	docker cp vsa-binaries-builder-run:/src/artifacts/. ./artifacts/
	ls artifacts
	docker rm vsa-binaries-builder-run
	export $(cat skaffold.env | xargs)
	skaffold build

.PHONY: clean-artifacts
clean-artifacts:
	rm -rf artifacts

# Individual service build targets
.PHONY: build-google-proxy
build-google-proxy:
	@echo "Building google-proxy service..."
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c 'go build -gcflags="all=-N -l" -o /src/app/google-proxy ./google-proxy'

.PHONY: build-oci-proxy
build-oci-proxy:
	@echo "Building oci-proxy service..."
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c 'go build -gcflags="all=-N -l" -o /src/app/oci-proxy ./oci-proxy'

.PHONY: build-vcm-proxy
build-vcm-proxy:
	@echo "Building vcm-proxy service..."
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c 'go build -gcflags="all=-N -l" -o /src/app/vcm-proxy ./vcm-proxy'

.PHONY: build-ontap-proxy
build-ontap-proxy:
	@echo "Building ontap-proxy service..."
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c 'go build -gcflags="all=-N -l" -o /src/app/ontap-proxy ./ontap-proxy'

.PHONY: build-core
build-core:
	@echo "Building core service..."
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c 'go build -gcflags="all=-N -l" -o /src/app/core ./vcp-core/cmd'

.PHONY: build-worker
build-worker:
	@echo "Building vcp-worker service..."
	docker build --build-arg GHVSA_PAT=$(GHVSA_PAT) -f builder/Dockerfile.build-all.dev -t vsa-binaries-builder builder
	mkdir -p app
	docker run --rm \
		-e GHVSA_PAT=$(GHVSA_PAT) \
		-v $(PWD):/src \
		-v $(GOCACHE):/go-build-cache \
		-v $(GOMODCACHE):/go/pkg/mod \
		-e GOCACHE=/go-build-cache \
		-e GOMODCACHE=/go/pkg/mod \
		vsa-binaries-builder sh -c 'go build -gcflags="all=-N -l" -o /src/app/vcp-worker ./worker'

.PHONY: base-image
base-image:
	@echo "Building base development image..."
	docker build -f common/Dockerfile.dev -t base:dev .

.PHONY: google-proxy-dev-image
google-proxy-dev-image: build-google-proxy base-image
	@echo "Building google-proxy development Docker image..."
	docker build --build-arg BASE=base:dev --build-arg GHVSA_PAT=$(GHVSA_PAT) -f google-proxy/Dockerfile.dev -t $(DEV_REGISTRY)/google-proxy:$(IMAGE_TAG) .

.PHONY: core-dev-image
core-dev-image: build-core base-image
	@echo "Building core development Docker image..."
	docker build --build-arg BASE=base:dev --build-arg GHVSA_PAT=$(GHVSA_PAT) -f vcp-core/Dockerfile.dev -t $(DEV_REGISTRY)/core:$(IMAGE_TAG) .

.PHONY: worker-dev-image
worker-dev-image: build-worker base-image
	@echo "Building vcp-worker development Docker image..."
	docker build --build-arg BASE=base:dev --build-arg GHVSA_PAT=$(GHVSA_PAT) -f worker/Dockerfile.dev -t $(DEV_REGISTRY)/vcp-worker:$(IMAGE_TAG) .

.PHONY: ontap-proxy-dev-image
ontap-proxy-dev-image: build-ontap-proxy base-image
	@echo "Building ontap-proxy development Docker image..."
	docker build --build-arg BASE=base:dev --build-arg GHVSA_PAT=$(GHVSA_PAT) -f ontap-proxy/Dockerfile.dev -t $(DEV_REGISTRY)/ontap-proxy:$(IMAGE_TAG) .

.PHONY: vcm-proxy-dev-image
vcm-proxy-dev-image: build-vcm-proxy base-image
	@echo "Building vcm-proxy development Docker image..."
	docker build --build-arg BASE=base:dev --build-arg GHVSA_PAT=$(GHVSA_PAT) -f vcm-proxy/Dockerfile.dev -t $(DEV_REGISTRY)/vcm-proxy:$(IMAGE_TAG) .

.PHONY: oci-proxy-dev-image
oci-proxy-dev-image: build-oci-proxy base-image
	@echo "Building oci-proxy development Docker image..."
	docker build --build-arg BASE=base:dev --build-arg GHVSA_PAT=$(GHVSA_PAT) -f oci-proxy/Dockerfile.dev -t $(DEV_REGISTRY)/oci-proxy:$(IMAGE_TAG) .

# Same layout as *-dev-image (DEV_REGISTRY, IMAGE_TAG, GHVSA_PAT) but service Dockerfile (expects artifacts/ + distroless base).
.PHONY: base-distroless-image
base-distroless-image:
	@echo "Building distroless base image (base:distroless)..."
	docker build -f common/Dockerfile -t base:distroless .

.PHONY: google-proxy-image
google-proxy-image: build-google-proxy base-distroless-image
	@echo "Building google-proxy image ($(DEV_REGISTRY)/google-proxy:$(IMAGE_TAG))..."
	mkdir -p artifacts && cp app/google-proxy artifacts/google-proxy
	docker build --build-arg BASE=base:distroless --build-arg GHVSA_PAT=$(GHVSA_PAT) -f google-proxy/Dockerfile -t $(DEV_REGISTRY)/google-proxy:$(IMAGE_TAG) .

.PHONY: core-image
core-image: build-core base-distroless-image
	@echo "Building core image ($(DEV_REGISTRY)/core:$(IMAGE_TAG))..."
	mkdir -p artifacts && cp app/core artifacts/core
	docker build --build-arg BASE=base:distroless --build-arg GHVSA_PAT=$(GHVSA_PAT) -f core/Dockerfile -t $(DEV_REGISTRY)/core:$(IMAGE_TAG) .

.PHONY: worker-image
worker-image: build-worker base-distroless-image
	@echo "Building vcp-worker image ($(DEV_REGISTRY)/vcp-worker:$(IMAGE_TAG))..."
	mkdir -p artifacts && cp app/vcp-worker artifacts/vcp-worker
	docker build --build-arg BASE=base:distroless --build-arg GHVSA_PAT=$(GHVSA_PAT) -f worker/Dockerfile -t $(DEV_REGISTRY)/vcp-worker:$(IMAGE_TAG) .

.PHONY: oci-proxy-image
oci-proxy-image: build-oci-proxy base-distroless-image
	@echo "Building oci-proxy image ($(DEV_REGISTRY)/oci-proxy:$(IMAGE_TAG))..."
	mkdir -p artifacts && cp app/oci-proxy artifacts/oci-proxy
	docker build --build-arg BASE=base:distroless --build-arg GHVSA_PAT=$(GHVSA_PAT) -f oci-proxy/Dockerfile -t $(DEV_REGISTRY)/oci-proxy:$(IMAGE_TAG) .

.PHONY: ontap-proxy-image
ontap-proxy-image: build-ontap-proxy base-distroless-image
	@echo "Building ontap-proxy image ($(DEV_REGISTRY)/ontap-proxy:$(IMAGE_TAG))..."
	mkdir -p artifacts && cp app/ontap-proxy artifacts/ontap-proxy
	docker build --build-arg BASE=base:distroless --build-arg GHVSA_PAT=$(GHVSA_PAT) -f ontap-proxy/Dockerfile -t $(DEV_REGISTRY)/ontap-proxy:$(IMAGE_TAG) .

.PHONY: vcm-proxy-image
vcm-proxy-image: build-vcm-proxy base-distroless-image
	@echo "Building vcm-proxy image ($(DEV_REGISTRY)/vcm-proxy:$(IMAGE_TAG))..."
	mkdir -p artifacts && cp app/vcm-proxy artifacts/vcm-proxy
	docker build --build-arg BASE=base:distroless --build-arg GHVSA_PAT=$(GHVSA_PAT) -f vcm-proxy/Dockerfile -t $(DEV_REGISTRY)/vcm-proxy:$(IMAGE_TAG) .

# Error Framework Validation
.PHONY: validate-errors
validate-errors:
	@echo "🔍 Running error framework validation..."
	@cd lib/errors && ./validate.sh

# SafeSQL targets
.PHONY: safesql-build
safesql-build:
	go build -o bin/safesql ./tools/safesql/

.PHONY: safesql-build-linux
safesql-build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/build/linux/safesql ./tools/safesql/

.PHONY: safesql-install
safesql-install:
	go install ./tools/safesql/

.PHONY: safesql-test
safesql-test:
	go test ./tools/safesql/...

# Quick error framework status check
.PHONY: error-status
error-status:
	@echo "📊 Error Framework Status"
	@echo "========================"
	@cd lib/errors && ./validate.sh --status-only 2>/dev/null || echo "Status check not available"

%:
	@:

.PHONY: test
test:
	@rm -f vcp-coverage.out
	@echo "mode: set" > vcp-coverage.out
	@for mod in $(WORKSPACE_MODULES); do \
		echo "==> testing $$mod"; \
		pkgs=$$(cd $$mod && go list ./... 2>/dev/null | grep -v scripts/sanity); \
		if [ -z "$$pkgs" ]; then echo "  (no packages, skipping)"; continue; fi; \
		( cd $$mod && go test -covermode=set -coverprofile=coverage.tmp.out $$pkgs ) || exit 1; \
		if [ -f $$mod/coverage.tmp.out ]; then \
			tail -n +2 $$mod/coverage.tmp.out >> vcp-coverage.out; \
			rm -f $$mod/coverage.tmp.out; \
		fi; \
	done

.PHONY: run-single-test
run-single-test:
	@for mod in $(WORKSPACE_MODULES); do \
		( cd $$mod && go test ./... -run $(TEST_NAME) ) || exit 1; \
	done

.PHONY: tidy
tidy:
	@for mod in $(WORKSPACE_MODULES); do \
		echo "==> tidying $$mod"; \
		( cd $$mod && go mod tidy ) || exit 1; \
	done

.PHONY: verify-tidy
verify-tidy: tidy
	@git diff --exit-code -- '**/go.mod' '**/go.sum' || { \
		echo "go.mod/go.sum drift detected. Run 'make tidy' and amend."; exit 1; \
	}