FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/clinker-vision-pipeline ./cmd/pipeline

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --system --uid 65532 --no-create-home --shell /usr/sbin/nologin clinker
COPY --from=build /out/clinker-vision-pipeline /usr/local/bin/clinker-vision-pipeline
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/clinker-vision-pipeline"]
