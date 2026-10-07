.PHONY: test race vet fmt lint

test:
	go test -count=1 ./...
race:
	go test -race ./...
vet:
	go vet ./...
	test -z "$$(gofmt -l .)"
	go mod tidy
	git diff --exit-code -- go.mod go.sum
fmt:
	gofmt -w cmd pkg
lint:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
