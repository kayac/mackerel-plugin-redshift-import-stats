LATEST_TAG := $(shell git describe --abbrev=0 --tags)

setup:
	go get \
		github.com/laher/goxc \
		github.com/tcnksm/ghr \
		github.com/golang/lint/golint
	go get -d -t ./...

test: setup
	go test -v ./...

lint: setup
	go vet ./...
	golint -set_exit_status ./...

dist:
	goxc

clean:
	rm -fr dist/*

release: dist
	ghr -u kayac -r mackerel-plugin-gunfish $(LATEST_TAG) dist/snapshot/

.PHONY: packages test lint clean setup dist
