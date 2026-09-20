#!/usr/bin/env bash
set -euo pipefail

pipeline_image="${PIPELINE_IMAGE:-clinker-vision-pipeline:rel-1}"
model_image="${MODEL_IMAGE:-clinker-vision-model:rel-1}"
output_dir="${1:-dist/images}"

docker compose build
mkdir -p "$output_dir"
docker save --output "$output_dir/pipeline.tar" "$pipeline_image"
docker save --output "$output_dir/model.tar" "$model_image"
sha256sum "$output_dir/pipeline.tar" "$output_dir/model.tar" > "$output_dir/SHA256SUMS"
echo "Exported $pipeline_image and $model_image to $output_dir"
