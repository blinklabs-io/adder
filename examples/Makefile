# Determine root directory
ROOT_DIR=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

# Gather all .go files for use in dependencies below
GO_FILES=$(shell find $(ROOT_DIR) -name '*.go')

# Gather list of expected binaries
BINARIES=$(shell cd $(ROOT_DIR) && find . -maxdepth 1 -type d ! -path . ! -path './.*' -exec basename {} \;)

.PHONY: build mod-tidy clean test format golines

# Alias for building program binaries
build: $(BINARIES)

mod-tidy:
	# Needed to fetch new dependencies and add them to go.mod
	cd $(ROOT_DIR)/.. && go mod tidy

clean:
	cd $(ROOT_DIR) && rm -f $(BINARIES)

format:
	cd $(ROOT_DIR) && go fmt ./...
	cd $(ROOT_DIR) && gofmt -s -w $(GO_FILES)

golines:
	cd $(ROOT_DIR) && golines -w --ignore-generated --chain-split-dots --max-len=80 --reformat-tags .

test:
	cd $(ROOT_DIR) && go test -v -race ./...

# Build our program binaries
# Depends on GO_FILES to determine when rebuild is needed
$(BINARIES): mod-tidy $(GO_FILES)
	cd $(ROOT_DIR)/$(@) && go build -o $(ROOT_DIR)/$(@) .
