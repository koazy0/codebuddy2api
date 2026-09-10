.PHONY: all build run clean fmt test

APP=codebuddy-gateway

all: build

build:
	CGO_ENABLED=1 go build -buildvcs=false -o $(APP) .

run: build
	./$(APP) server

test:
	go test ./...

fmt:
	go fmt ./...

clean:
	rm -f $(APP)
