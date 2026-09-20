FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/clinker-vision-mock-model ./cmd/mock-model

FROM debian:bookworm-slim

RUN useradd --system --uid 65532 --no-create-home --shell /usr/sbin/nologin clinker
COPY --from=build /out/clinker-vision-mock-model /usr/local/bin/clinker-vision-mock-model
USER 65532:65532
EXPOSE 50051
ENTRYPOINT ["/usr/local/bin/clinker-vision-mock-model"]
