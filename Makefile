PKG=...

setup:
	go mod tidy

test:
	go test -race -vet=all -covermode=atomic -coverprofile=cover.out ./$(PKG)

test-linux:
	docker run -it --rm \
			-v `pwd`:/go/src/github.com/ghettovoice/gosip \
			-v ~/.ssh:/root/.ssh \
			-w /go/src/github.com/ghettovoice/gosip \
			golang:latest \
			go version && \
			make setup && make test

lint:
	go tool golangci-lint run ./...
	go tool govulncheck ./...

cov:
	go tool cover -html=./cover.out

docs:
	go tool doc -http

gen:
	go generate ./...

# Release a new version
# Usage: make release VERSION=vX.Y.Z
release:
	@if [ -z "$(VERSION)" ]; then \
		echo "Error: VERSION is not set. Usage: make release VERSION=vX.Y.Z" >&2; \
		exit 1; \
	fi
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[0-9]+)?$$'; then \
		echo "Error: Invalid version format. Use semantic versioning (e.g., v1.2.3, v1.2.3-alpha.1, v1.2.3-beta.2, v1.2.3-rc.3)" >&2; \
		exit 1; \
	fi
	@echo "Updating version to $(VERSION) in gosip.go..."
	@sed -i '' 's/^const VERSION = ".*"/const VERSION = "$(VERSION)"/' gosip.go
	git add gosip.go
	git commit -m "Release $(VERSION)"
	git tag -a $(VERSION) -m "Release $(VERSION)"
	@echo "\nRelease $(VERSION) is ready to be pushed. Run the following command to publish:"
	@echo "  git push --follow-tags"

bench: PKG=
bench:
	$(eval PREFIX := $(shell if [ "$(PKG)" = "..." ] || [ "$(PKG)" = "." ] || [ "$(PKG)" = "" ]; then echo "gosip_"; else echo "$(PKG)" | sed 's#/#_#g'; fi ))
	$(eval SUFFIX := $(shell echo "_$(shell date +%Y%m%d%H%M%S)"))
	go test -vet=all -run=^$$ -bench=. -benchmem -count=10 \
		-memprofile=$(PREFIX)mem$(SUFFIX).out \
		-cpuprofile=$(PREFIX)cpu$(SUFFIX).out \
		./$(PKG) \
	| tee $(PREFIX)bench$(SUFFIX).out
