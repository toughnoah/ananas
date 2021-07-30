.PHONY: all
all: fmt vet install docker

IMG_VER= v1.0
GOPROXYADDR = https://goproxy.io
BUILD_PATH = cmd

fmt: ## Run go fmt against code.
	@go fmt ./...

vet: ## Run go vet against code.
	@go vet ./...

.PHONY: install
install:
	@export GO111MODULE=on
	@export GOPROXY=$(GOPROXYADDR)
	@go mod tidy
	@go build -o $(BUILD_PATH)/ananas $(BUILD_PATH)/main.go

.PHONY: docker
docker:
	@docker build -t toughnoah/ananas:$(IMG_VER) ./$(BUILD_PATH)
	@docker push toughnoah/ananas:$(IMG_VER)

.PHONY: test
test:
	@go test ./... -v -coverprofile=cover.out -ginkgo.v