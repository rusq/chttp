SHELL=/bin/sh

test:
	go test ./... -race -cover
.PHONY: test

test_all: 
	CHTTP_RUN_INTEGRATION_TESTS=1 go test ./... -race -cover
.PHONY: test_all
