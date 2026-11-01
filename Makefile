.PHONY: build test race vet release clean

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -o dist/proxrun ./cmd/proxrun

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

release:
	mkdir -p dist
	@for os in linux darwin; do \
		for arch in amd64 arm64; do \
			echo "Building $$os/$$arch"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags='-s -w' -o dist/proxrun-$$os-$$arch ./cmd/proxrun || exit 1; \
		done; \
	done

clean:
	rm -rf dist
