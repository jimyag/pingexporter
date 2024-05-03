build:
	go build -o pingexporter -v --trimpath -ldflags "-s -w" ./