.PHONY: build build-opt clean cross-build

BINARY_NAME=devproxy
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-X github.com/daydaychen/devproxy/pkg/util.Version=$(VERSION)
BUILD_DIR=build
PLATFORMS=linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) main.go

build-opt:
	# -s: 禁用符号表, -w: 禁用 DWARF 生成, 减小体积
	go build -ldflags="-s -w $(LDFLAGS)" -o $(BINARY_NAME) main.go

cross-build:
	mkdir -p $(BUILD_DIR)
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} ; \
		GOARCH=$${platform#*/} ; \
		OUTPUT=$(BUILD_DIR)/$(BINARY_NAME)-$$GOOS-$$GOARCH ; \
		if [ "$$GOOS" = "windows" ]; then OUTPUT="$$OUTPUT.exe"; fi ; \
		echo "Building for $$GOOS/$$GOARCH..." ; \
		GOOS=$$GOOS GOARCH=$$GOARCH go build -ldflags="-s -w $(LDFLAGS)" -o $$OUTPUT main.go ; \
	done

release: build-opt
	mkdir -p $(HOME)/.local/bin
	mv $(BINARY_NAME) $(HOME)/.local/bin/

clean:
	go clean
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)
