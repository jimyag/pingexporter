version=$(shell git describe --tags --always)
build:
	go build -o pingexporter -v --trimpath -ldflags "-s -w -X github.com/jimyag/version-go.version=$(version) -X github.com/jimyag/version-go.enableCmd=true" ./