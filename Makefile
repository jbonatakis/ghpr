BINARY := ghpr

.PHONY: build test vet fmt run clean

build:
	go build -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)
