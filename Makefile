VERSION=$(shell git describe --tags)
PKG_NAME=github.com/ghettovoice/gosip
LDFLAGS=-ldflags "-X gosip.Version=${VERSION}"
GOFLAGS=

install:
	cd $$GOPATH/src/$(PKG_NAME); \
	go get -v github.com/onsi/ginkgo/ginkgo; \
  go get -v github.com/onsi/gomega; \
  go get -v -t ./...; \
  go install $(LDFLAGS)

test:
	cd $$GOPATH/src/$(PKG_NAME); \
	ginkgo -r --randomizeAllSpecs --randomizeSuites --cover --trace --race --compilers=2 --succinct --progress $(GOFLAGS)

test-%:
	cd $$GOPATH/src/$(PKG_NAME); \
	ginkgo -r --randomizeAllSpecs --randomizeSuites --cover --trace --race --compilers=2 --progress $(GOFLAGS) ./$*

test-watch:
	cd $$GOPATH/src/$(PKG_NAME); \
	ginkgo watch -r --trace --race $(GOFLAGS)

test-watch-%:
	cd $$GOPATH/src/$(PKG_NAME); \
	ginkgo watch -r --trace --race $(GOFLAGS) ./$*

format:
	cd $$GOPATH/src/$(PKG_NAME); \
	go fmt -w *.go
