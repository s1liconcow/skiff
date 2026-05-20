#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "usage: $0 dist/runner-image-*-manifest.json" >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required to read Packer manifest files" >&2
  exit 2
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "aws CLI is required to publish SSM parameters" >&2
  exit 2
fi

for manifest in "$@"; do
  if [ ! -f "$manifest" ]; then
    echo "manifest not found: $manifest" >&2
    exit 2
  fi

  arch="$(jq -r '.builds[0].custom_data.ssm_arch // empty' "$manifest")"
  version="$(jq -r '.builds[0].custom_data.version // empty' "$manifest")"
  channel="$(jq -r '.builds[0].custom_data.channel // "stable"' "$manifest")"
  prefix="$(jq -r '.builds[0].custom_data.ssm_prefix // "/skiff/runner/ami/al2023"' "$manifest")"

  if [ -z "$arch" ] || [ -z "$version" ]; then
    echo "manifest $manifest is missing arch or version custom_data" >&2
    exit 2
  fi

  channel_parameter="${prefix}/${arch}/${channel}"
  version_parameter="${prefix}/${arch}/${version}"

  while IFS= read -r artifact_id; do
    IFS=',' read -ra artifacts <<< "$artifact_id"
    for artifact in "${artifacts[@]}"; do
      region="${artifact%%:*}"
      ami_id="${artifact#*:}"
      if [ -z "$region" ] || [ -z "$ami_id" ] || [ "$region" = "$ami_id" ]; then
        echo "unexpected artifact_id entry in $manifest: $artifact" >&2
        exit 2
      fi

      for parameter in "$version_parameter" "$channel_parameter"; do
        aws --region "$region" ssm put-parameter \
          --name "$parameter" \
          --type String \
          --value "$ami_id" \
          --overwrite \
          --tier Standard >/dev/null

        aws --region "$region" ssm add-tags-to-resource \
          --resource-type Parameter \
          --resource-id "${parameter#/}" \
          --tags \
            "Key=skiff.dev/managed,Value=true" \
            "Key=skiff.dev/role,Value=runner" \
            "Key=skiff.dev/version,Value=${version}" \
            "Key=skiff.dev/arch,Value=${arch}" >/dev/null
      done

      echo "published ${ami_id} to ${region}: ${version_parameter}, ${channel_parameter}"
    done
  done < <(jq -r '.builds[].artifact_id // empty' "$manifest")
done
