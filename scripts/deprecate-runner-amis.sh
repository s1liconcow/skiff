#!/usr/bin/env bash
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required to select runner AMIs" >&2
  exit 2
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "aws CLI is required to deprecate AMIs" >&2
  exit 2
fi

regions="${SKIFF_RUNNER_AMI_REGIONS:-${AWS_REGION:-${AWS_DEFAULT_REGION:-}}}"
keep="${SKIFF_RUNNER_AMI_KEEP:-5}"
apply="${SKIFF_RUNNER_AMI_DEPRECATE:-0}"
deprecate_at="${SKIFF_RUNNER_AMI_DEPRECATE_AT:-}"

if [ -z "$regions" ]; then
  echo "set SKIFF_RUNNER_AMI_REGIONS or AWS_REGION" >&2
  exit 2
fi

if [ -z "$deprecate_at" ]; then
  if deprecate_at="$(date -u -d '+90 days' '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null)"; then
    :
  elif deprecate_at="$(date -u -v+90d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null)"; then
    :
  else
    echo "could not compute default deprecation time; set SKIFF_RUNNER_AMI_DEPRECATE_AT" >&2
    exit 2
  fi
fi

for region in $regions; do
  images="$(aws --region "$region" ec2 describe-images \
    --owners self \
    --filters \
      Name=tag:skiff.dev/managed,Values=true \
      Name=tag:skiff.dev/role,Values=runner \
    --query 'Images | sort_by(@, &CreationDate) | reverse(@)' \
    --output json)"

  old_images="$(jq -r ".[$keep:][]?.ImageId" <<< "$images")"
  if [ -z "$old_images" ]; then
    echo "region ${region}: no runner AMIs beyond keep=${keep}"
    continue
  fi

  while IFS= read -r image_id; do
    if [ -z "$image_id" ]; then
      continue
    fi
    if [ "$apply" = "1" ]; then
      aws --region "$region" ec2 enable-image-deprecation \
        --image-id "$image_id" \
        --deprecate-at "$deprecate_at" >/dev/null
      echo "region ${region}: scheduled ${image_id} deprecation at ${deprecate_at}"
    else
      echo "region ${region}: would deprecate ${image_id} at ${deprecate_at}"
    fi
  done <<< "$old_images"
done
