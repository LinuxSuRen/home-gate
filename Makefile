.PHONY: all web build test clean

all: web build

web:
	cd web && npm install --no-audit --no-fund && npm run build

build:
	CGO_ENABLED=0 go build -o home-gate .

test:
	go test ./...

clean:
	rm -f home-gate
