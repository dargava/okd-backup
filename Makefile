BINARY := okd-backup

.PHONY: build clean

build:
	go build -ldflags="-s -w" -o $(BINARY) .

clean:
	rm -f $(BINARY)
