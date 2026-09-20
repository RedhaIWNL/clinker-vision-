# clinker-vision-model — gRPC inference service (contract v0.3, package v2).
# Build:  docker build -t clinker-vision-model:rel-1 .
# Run:    docker run --rm -p 50051:50051 clinker-vision-model:rel-1
# Mock:   docker run --rm -p 50051:50051 clinker-vision-model:rel-1 \
#           python -m src.server --bind 0.0.0.0:50051 --mock
FROM python:3.10-slim

WORKDIR /app

# Toolchain first (better layer cache): no network access happens at runtime,
# only here at image build time.
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY contract/ contract/
COPY src/ src/

# Self-contained image: generate protobuf stubs at build time.
RUN mkdir -p src/gen && touch src/gen/__init__.py && \
    python -m grpc_tools.protoc -Icontract \
        --python_out=src/gen --grpc_python_out=src/gen \
        contract/inference_v2.proto

EXPOSE 50051

# The model only needs write access to the separately mounted /app/state path.
RUN useradd --system --uid 65532 --no-create-home --shell /usr/sbin/nologin clinker
USER 65532:65532

CMD ["python", "-m", "src.server", "--bind", "0.0.0.0:50051"]
