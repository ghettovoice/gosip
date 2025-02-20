GINKGO_FLAGS=
GINKGO_BASE_FLAGS=-r -p --randomize-all --trace --race --vet="" --covermode=atomic --coverprofile=cover.profile
GINKGO_TEST_FLAGS=${GINKGO_BASE_FLAGS} --randomize-suites
GINKGO_WATCH_FLAGS=${GINKGO_BASE_FLAGS}

PKG_PATH=

setup:
	go get -v -t ./...
	go install -mod=mod github.com/ghettovoice/abnf/...
	go install -mod=mod github.com/onsi/ginkgo/v2/ginkgo
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

test:
	@ginkgo version
	ginkgo $(GINKGO_TEST_FLAGS) $(GINKGO_FLAGS) ./$(PKG_PATH)

test-linux:
	docker run -it --rm \
			-v `pwd`:/go/src/github.com/ghettovoice/gosip \
			-v ~/.ssh:/root/.ssh \
			-w /go/src/github.com/ghettovoice/gosip \
			golang:latest \
			go version && \
			make setup && make test

watch:
	@ginkgo version
	ginkgo watch $(GINKGO_WATCH_FLAGS) $(GINKGO_FLAGS) ./$(PKG_PATH)

lint:
	golangci-lint run -v ./...
	govulncheck -version ./...

cov:
	go tool cover -html=./cover.profile

doc:
	@echo "Running documentation on http://localhost:8080/github.com/ghettovoice/gosip"
	pkgsite -http=localhost:8080

gram-gen:
	abnf gen -y -c ./sip/internal/grammar/rfc3966/abnf.yml
	abnf gen -y -c ./sip/internal/grammar/rfc3261/abnf.yml
