VERSION=$(shell git describe --tags)
PKG_NAME=github.com/ghettovoice/gosip
LDFLAGS=-ldflags "-X gosip.Version=${VERSION}"
GOFLAGS=

install:
	cd $$GOPATH/src/$(PKG_NAME); \
	go get -v -t ./...; \
	go install $(LDFLAGS)

test: test-core test-syntax test-timing test-transport
	cd $$GOPATH/src/$(PKG_NAME); \
	go test -race $(GOFLAGS)

test-%:
	cd $$GOPATH/src/$(PKG_NAME); \
	go test -race $(GOFLAGS) ./$*

test-ginkgo:
	ginkgo -r --randomizeAllSpecs --randomizeSuites --failOnPending --cover --trace --race --compilers=2 --succinct --progress

format:
	cd $$GOPATH/src/$(PKG_NAME); \
	go fmt -w *.go
