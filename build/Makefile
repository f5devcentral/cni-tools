all: binary_build

binary_build: build_linux build_darwin

dt=$(shell date +%Y%m%d)
build_linux:
	cd ..; \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -ldflags '-s -w --extldflags "-static -fpic"' -o f5-tool-setup-cni-linux-$(dt)

build_darwin:
	cd ..; \
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 \
	go build -ldflags '-s -w --extldflags "-static -fpic"' -o f5-tool-setup-cni-darwin-$(dt)
