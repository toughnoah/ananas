.PHONY: all
all: docker

.PHONY: docker
docker:
	@go build -o ananas
	@docker build -t toughnoah/ananas:v1.0 .
	@docker push toughnoah/ananas:v1.0