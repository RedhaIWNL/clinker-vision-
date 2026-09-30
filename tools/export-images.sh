#!/usr/bin/env bash
# Offline delivery (no internet on the target): save the images of one version to files.
#   CV_VERSION=v2026.09.30 tools/export-images.sh [dir]   (images downloaded from GitHub)
#   tools/export-images.sh [dir]                           (CV_VERSION unset: builds "dev" locally)
# On the target: docker load -i pipeline.tar; docker load -i model.tar; then CV_VERSION in .env.
set -euo pipefail

tag="${CV_VERSION:-dev}"
pipeline_image="${PIPELINE_REPO:-ghcr.io/redhaiwnl/clinker-vision-pipeline}:$tag"
model_image="${MODEL_REPO:-ghcr.io/redhaiwnl/clinker-vision-model}:$tag"
output_dir="${1:-dist/images}"

if [ "$tag" = dev ]; then docker compose build; else docker compose pull; fi
mkdir -p "$output_dir"
docker save --output "$output_dir/pipeline.tar" "$pipeline_image"
docker save --output "$output_dir/model.tar" "$model_image"
sha256sum "$output_dir/pipeline.tar" "$output_dir/model.tar" > "$output_dir/SHA256SUMS"
echo "Exported $pipeline_image and $model_image to $output_dir"
