# Determine root directory
ROOT_DIR=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

# Gather all .go files for use in dependencies below
GO_FILES=$(shell find $(ROOT_DIR) -name '*.go')

# Gather list of expected binaries
BINARIES=$(shell cd $(ROOT_DIR)/cmd && ls -1 | grep -v ^common | grep -v ^adder-tray)

# Extract Go module name from go.mod
GOMODULE=$(shell grep ^module $(ROOT_DIR)/go.mod | awk '{ print $$2 }')

# Set version strings based on git tag and current ref
GO_LDFLAGS=-ldflags "-s -w -X '$(GOMODULE)/internal/version.Version=$(shell git describe --tags --exact-match 2>/dev/null)' -X '$(GOMODULE)/internal/version.CommitHash=$(shell git rev-parse --short HEAD)'"

GO_CGO_CFLAGS=$(shell go env CGO_CFLAGS)
TRAY_CGO_CFLAGS=$(strip $(GO_CGO_CFLAGS) $(if $(filter windows/arm64,$(GOOS)/$(GOARCH)),-DWINBOOL=BOOL,))

.PHONY: build build-tray mod-tidy clean test bundle-macos pkg-macos pkg-macos-adhoc

# Alias for building program binary
build: $(BINARIES)

# Create a local Adder.app for macOS
bundle-macos:
	./bundle-macos.sh

# Build a (signed + notarized when secrets are set) macOS .pkg installer
# containing both adder and adder-tray inside Adder.app. See
# packaging/macos/README.md for the env-var contract.
pkg-macos:
	./packaging/macos/build-pkg.sh

# Build an ad-hoc-signed macOS .pkg for LOCAL testing (no Developer ID needed).
# The bundle is ad-hoc signed so the app runs and notifications work; the pkg
# itself is unsigned (install via `sudo installer -pkg <pkg> -target /`).
pkg-macos-adhoc:
	ADHOC=1 ./packaging/macos/build-pkg.sh

mod-tidy:
	# Needed to fetch new dependencies and add them to go.mod
	go mod tidy

clean:
	rm -f $(BINARIES)

format: mod-tidy
	go fmt ./...
	gofmt -s -w $(GO_FILES)

golines:
	golines -w --ignore-generated --chain-split-dots --max-len=80 --reformat-tags .

swagger:
	swag f -g api.go -d api,output
	swag i -g api.go -d api,output

test: mod-tidy
	go test -v -race ./...

# Build adder-tray binary
# CGO is required on all platforms for Fyne UI support.
build-tray: mod-tidy $(GO_FILES)
	CGO_CFLAGS="$(TRAY_CGO_CFLAGS)" CGO_ENABLED=1 go build \
		$(GO_LDFLAGS) \
		-o adder-tray$(if $(filter windows,$(GOOS)),.exe,) \
		./cmd/adder-tray

# Build our program binaries
# Depends on GO_FILES to determine when rebuild is needed
$(BINARIES): mod-tidy $(GO_FILES)
	CGO_ENABLED=0 go build \
		$(GO_LDFLAGS) \
		-tags nodbus \
		-o $(@)$(if $(filter windows,$(GOOS)),.exe,)  \
		./cmd/$(@)
