.PHONY: build test vet check clean

build:
	go build -o bin/hostix ./cmd/hostix

test:
	go test -race ./...

vet:
	go vet ./...

check: vet test

clean:
	go clean
	rm -rf bin coverage.out coverage.html
