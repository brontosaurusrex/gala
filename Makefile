.PHONY: build test clean

build:
	go build -o gala .

test:
	go test ./...

clean:
	rm -f gala
