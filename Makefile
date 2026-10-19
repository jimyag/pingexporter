version=$(shell git describe --tags --always)
ldflags="-s -w -X github.com/jimmicro/version.GitTag=$(version)"
build:
	go build -o pingexporter -v --trimpath -ldflags ${ldflags} ./
arm64:
	CGO_ENABLED=0 GOARCH=arm64 go build -o pingexporter -v --trimpath -ldflags ${ldflags} ./
