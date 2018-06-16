VERSION=$(shell git describe --tags)
PKG_NAME=github.com/ghettovoice/gosip
LDFLAGS=-ldflags "-X gosip.Version=${VERSION}"
GOFLAGS=

install:
	cd $$GOPATH/src/$(PKG_NAME); \
	go get -v \
		github.com/onsi/ginkgo/ginkgo \
		github.com/onsi/gomega \
		github.com/wadey/gocovmerge; \
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

cover-report: cover-merge
	cd $$GOPATH/src/$(PKG_NAME); \
	go tool cover -html=./gosip.full.coverprofile

cover-merge:
	cd $$GOPATH/src/$(PKG_NAME); \
	gocovmerge \
		./gosip.coverprofile \
		./sip/sip.coverprofile \
		./sip/parser/parser.coverprofile \
		./timing/timing.coverprofile \
		./transaction/transaction.coverprofile \
		./transport/transport.coverprofile \
	> ./gosip.full.coverprofile

format:
	cd $$GOPATH/src/$(PKG_NAME); \
	go fmt *.go
