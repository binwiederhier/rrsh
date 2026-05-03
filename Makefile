.PHONY: help \
        build build-linux-amd64 build-linux-armv6 build-linux-armv7 build-linux-arm64 \
        release release-snapshot \
        test clean deps \
        install-linux-amd64 install-linux-armv6 install-linux-armv7 install-linux-arm64 \
        install-linux-amd64-deb install-linux-armv6-deb install-linux-armv7-deb install-linux-arm64-deb \
        remove-binary purge-package

help:
	@echo "Build (using GoReleaser):"
	@echo "  make build                      - Build noshell for all architectures"
	@echo "  make build-linux-amd64          - Build noshell (Linux, amd64 only)"
	@echo "  make build-linux-armv6          - Build noshell (Linux, armv6 only)"
	@echo "  make build-linux-armv7          - Build noshell (Linux, armv7 only)"
	@echo "  make build-linux-arm64          - Build noshell (Linux, arm64 only)"
	@echo
	@echo "Test/clean:"
	@echo "  make test                       - Run tests"
	@echo "  make clean                      - Remove dist/ folder"
	@echo "  make deps                       - Install GoReleaser"
	@echo
	@echo "Releasing:"
	@echo "  make release                    - Create a release"
	@echo "  make release-snapshot           - Create a test release"
	@echo
	@echo "Install locally (requires sudo, expects dist/ from goreleaser):"
	@echo "  make install-linux-amd64        - Copy amd64 binary from dist/ to /usr/bin/noshell"
	@echo "  make install-linux-armv6        - Copy armv6 binary from dist/ to /usr/bin/noshell"
	@echo "  make install-linux-armv7        - Copy armv7 binary from dist/ to /usr/bin/noshell"
	@echo "  make install-linux-arm64        - Copy arm64 binary from dist/ to /usr/bin/noshell"
	@echo "  make install-linux-amd64-deb    - Install .deb from dist/ (amd64 only)"
	@echo "  make install-linux-armv6-deb    - Install .deb from dist/ (armv6 only)"
	@echo "  make install-linux-armv7-deb    - Install .deb from dist/ (armv7 only)"
	@echo "  make install-linux-arm64-deb    - Install .deb from dist/ (arm64 only)"

build: deps
	goreleaser build --snapshot --clean

build-linux-amd64: deps
	goreleaser build --snapshot --clean --id noshell_linux_amd64

build-linux-armv6: deps
	goreleaser build --snapshot --clean --id noshell_linux_armv6

build-linux-armv7: deps
	goreleaser build --snapshot --clean --id noshell_linux_armv7

build-linux-arm64: deps
	goreleaser build --snapshot --clean --id noshell_linux_arm64

release: clean deps test
	goreleaser release --clean

release-snapshot: clean deps test
	goreleaser release --snapshot --clean

test:
	go test ./...

clean:
	rm -rf dist

deps:
	which goreleaser >/dev/null || go install github.com/goreleaser/goreleaser/v2@latest

install-linux-amd64: remove-binary
	sudo cp -a dist/noshell_linux_amd64_linux_amd64_v1/noshell /usr/bin/noshell

install-linux-armv6: remove-binary
	sudo cp -a dist/noshell_linux_armv6_linux_arm_6/noshell /usr/bin/noshell

install-linux-armv7: remove-binary
	sudo cp -a dist/noshell_linux_armv7_linux_arm_7/noshell /usr/bin/noshell

install-linux-arm64: remove-binary
	sudo cp -a dist/noshell_linux_arm64_linux_arm64/noshell /usr/bin/noshell

remove-binary:
	sudo rm -f /usr/bin/noshell

install-linux-amd64-deb: purge-package
	sudo dpkg -i dist/noshell_*_linux_amd64.deb

install-linux-armv6-deb: purge-package
	sudo dpkg -i dist/noshell_*_linux_armv6.deb

install-linux-armv7-deb: purge-package
	sudo dpkg -i dist/noshell_*_linux_armv7.deb

install-linux-arm64-deb: purge-package
	sudo dpkg -i dist/noshell_*_linux_arm64.deb

purge-package:
	sudo apt-get purge noshell || true
