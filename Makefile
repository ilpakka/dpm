.PHONY: all clean embed dpm dpm-linux dpm_check tui deploy

DPM_HOST = dpm.fi
DPM_USER = henry
WEB_ROOT = /var/www/html

all: dpm dpm_check

# Copy catalog/profiles into cmd/dpm/ for go:embed
embed:
	@rm -rf cmd/dpm/catalog cmd/dpm/profiles
	@cp -r catalog cmd/dpm/catalog
	@cp -r profiles cmd/dpm/profiles

# Build Go dpm binary (local platform)
dpm: embed
	go build -o dpm ./cmd/dpm

# Cross-compile for linux-amd64
dpm-linux: embed
	GOOS=linux GOARCH=amd64 go build -o dpm-linux-amd64 ./cmd/dpm

# Build C dpm_check binary
dpm_check:
	$(MAKE) -C dpm_check

# Build the Rust + Ratatui frontend (dpm-tui)
tui:
	cd tui && cargo build --release

# Deploy to production (dpm.fi)
# Cross-compiles dpm for all platforms and uploads binaries + install.sh
deploy: embed
	@echo "=== Building all platforms ==="
	GOOS=linux  GOARCH=amd64 go build -o /tmp/dpm-linux-amd64  ./cmd/dpm
	GOOS=linux  GOARCH=arm64 go build -o /tmp/dpm-linux-arm64  ./cmd/dpm
	GOOS=darwin GOARCH=arm64 go build -o /tmp/dpm-darwin-arm64 ./cmd/dpm
	GOOS=darwin GOARCH=amd64 go build -o /tmp/dpm-darwin-amd64 ./cmd/dpm
	@echo "=== Uploading to $(DPM_HOST) ==="
	scp /tmp/dpm-linux-amd64 /tmp/dpm-linux-arm64 /tmp/dpm-darwin-arm64 /tmp/dpm-darwin-amd64 install.sh dpm:/tmp/
	ssh dpm " \
		cp /tmp/dpm-linux-amd64  $(WEB_ROOT)/packages/linux-amd64/dpm && \
		cp /tmp/dpm-linux-arm64  $(WEB_ROOT)/packages/linux-arm64/dpm && \
		cp /tmp/dpm-darwin-arm64 $(WEB_ROOT)/packages/darwin-arm64/dpm && \
		cp /tmp/dpm-darwin-amd64 $(WEB_ROOT)/packages/darwin-amd64/dpm && \
		chmod +x $(WEB_ROOT)/packages/*/dpm && \
		cp /tmp/install.sh $(WEB_ROOT)/install.sh && \
		rm /tmp/dpm-linux-* /tmp/dpm-darwin-* /tmp/install.sh"
	@rm /tmp/dpm-linux-* /tmp/dpm-darwin-*
	@echo "=== Deployed to https://$(DPM_HOST) ==="

clean:
	rm -f dpm dpm-linux-amd64
	rm -rf cmd/dpm/catalog cmd/dpm/profiles
	$(MAKE) -C dpm_check clean
