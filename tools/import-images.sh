#!/usr/bin/env bash
set -euo pipefail

input_dir="${1:-dist/images}"
sha256sum --check "$input_dir/SHA256SUMS"
docker load --input "$input_dir/pipeline.tar"
docker load --input "$input_dir/model.tar"
