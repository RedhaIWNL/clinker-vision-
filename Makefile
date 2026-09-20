.PHONY: fmt test race vet check compose-config proto proto-v2

fmt:
	test -z "$$(gofmt -l .)"

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

compose-config:
	docker compose config

proto:
	PATH="$$(go env GOPATH)/bin:$$PATH" protoc -I proto -I /usr/include --go_out=paths=source_relative:internal/inference/gen --go-grpc_out=paths=source_relative:internal/inference/gen proto/inference.proto

proto-v2:
	mkdir -p internal/inference/gen/v2
	PATH="$$(go env GOPATH)/bin:$$PATH" protoc -I proto -I /usr/include --go_out=paths=source_relative:internal/inference/gen/v2 --go-grpc_out=paths=source_relative:internal/inference/gen/v2 proto/inference_v2.proto

check: fmt test race vet compose-config
