VERSION=$(shell git describe --tags)
LDFLAGS=-ldflags "-X gosip.Version=${VERSION}"
GOFLAGS=

install: .install-utils
	go get -v -t ./...
	go mod tidy

.install-utils:
	go get -v github.com/wadey/gocovmerge
	go get -v github.com/onsi/ginkgo/...
	go get -v github.com/onsi/gomega/...

test:
	ginkgo -r --randomizeAllSpecs --randomizeSuites --cover --trace --race --compilers=2 --progress $(GOFLAGS)

test-%:
	ginkgo -r --randomizeAllSpecs --randomizeSuites --cover --trace --race --compilers=2 --progress $(GOFLAGS) ./$*

test-watch:
	ginkgo watch -r --trace --race $(GOFLAGS)

test-watch-%:
	ginkgo watch -r --trace --race $(GOFLAGS) ./$*

test-linux:
	docker run -it --rm \
			-v `pwd`:/go/src/github.com/ghettovoice/gosip \
			-v ~/.ssh:/root/.ssh \
			-w /go/src/github.com/ghettovoice/gosip \
			golang:stretch \
			make install && make test

cover-report: cover-merge
	go tool cover -html=./gosip.full.coverprofile

cover-merge:
	gocovmerge \
		./gosip.coverprofile \
		./sip/sip.coverprofile \
		./sip/parser/parser.coverprofile \
		./timing/timing.coverprofile \
		./transaction/transaction.coverprofile \
		./transport/transport.coverprofile \
	> ./gosip.full.coverprofile
