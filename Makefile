.PHONY: all
all: docker

.PHONY: docker
docker:
	@go build -o cmd/ananas cmd/ananas.go
	@docker build -t toughnoah/ananas:v1.0 ./cmd
	@docker push toughnoah/ananas:v1.0