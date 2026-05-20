variable "region" {
  type    = string
  default = "us-west-2"
}

variable "ami_regions" {
  type    = list(string)
  default = []
}

variable "skiff_version" {
  type = string
}

variable "artifact_dir" {
  type    = string
  default = "dist"
}

variable "channel" {
  type    = string
  default = "stable"
}

variable "provenance_commit" {
  type    = string
  default = "unknown"
}

variable "provenance_source" {
  type    = string
  default = "github.com/s1liconcow/skiff"
}

variable "ssm_parameter_prefix" {
  type    = string
  default = "/skiff/runner/ami/al2023"
}

packer {
  required_plugins {
    amazon = {
      version = ">= 1.3.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

locals {
  common_tags = {
    "skiff.dev/managed"           = "true"
    "skiff.dev/role"              = "runner"
    "skiff.dev/version"           = var.skiff_version
    "skiff.dev/provenance-source" = var.provenance_source
    "skiff.dev/provenance-commit" = var.provenance_commit
  }
}

source "amazon-ebs" "runner_amd64" {
  region        = var.region
  instance_type = "t3.micro"
  ssh_username  = "ec2-user"

  source_ami_filter {
    filters = {
      architecture        = "x86_64"
      name                = "al2023-ami-2023.*-kernel-6.1-x86_64"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    owners      = ["amazon"]
    most_recent = true
  }

  ami_name        = "skiff-runner-${var.skiff_version}-al2023-x86-64-{{timestamp}}"
  ami_description = "Skiff runner ${var.skiff_version} on Amazon Linux 2023 x86_64"
  ami_regions     = var.ami_regions

  tags = merge(local.common_tags, {
    "skiff.dev/arch"          = "x86_64"
    "skiff.dev/channel"       = var.channel
    "skiff.dev/ssm-parameter" = "${var.ssm_parameter_prefix}/x86_64/${var.channel}"
  })

  run_tags = merge(local.common_tags, {
    "skiff.dev/arch" = "x86_64"
  })

  snapshot_tags = merge(local.common_tags, {
    "skiff.dev/arch" = "x86_64"
  })
}

source "amazon-ebs" "runner_arm64" {
  region        = var.region
  instance_type = "t4g.micro"
  ssh_username  = "ec2-user"

  source_ami_filter {
    filters = {
      architecture        = "arm64"
      name                = "al2023-ami-2023.*-kernel-6.1-arm64"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    owners      = ["amazon"]
    most_recent = true
  }

  ami_name        = "skiff-runner-${var.skiff_version}-al2023-arm64-{{timestamp}}"
  ami_description = "Skiff runner ${var.skiff_version} on Amazon Linux 2023 arm64"
  ami_regions     = var.ami_regions

  tags = merge(local.common_tags, {
    "skiff.dev/arch"          = "arm64"
    "skiff.dev/channel"       = var.channel
    "skiff.dev/ssm-parameter" = "${var.ssm_parameter_prefix}/arm64/${var.channel}"
  })

  run_tags = merge(local.common_tags, {
    "skiff.dev/arch" = "arm64"
  })

  snapshot_tags = merge(local.common_tags, {
    "skiff.dev/arch" = "arm64"
  })
}

build {
  name    = "runner-amd64"
  sources = ["source.amazon-ebs.runner_amd64"]

  provisioner "file" {
    source      = "${var.artifact_dir}/skiff_${var.skiff_version}_linux_amd64.tar.gz"
    destination = "/tmp/skiff.tar.gz"
  }

  provisioner "file" {
    source      = "build/runner-image/systemd/skiff-runner.service"
    destination = "/tmp/skiff-runner.service"
  }

  provisioner "file" {
    source      = "build/runner-image/collector.yaml"
    destination = "/tmp/collector.yaml"
  }

  provisioner "shell" {
    inline_shebang = "/bin/bash -euxo pipefail"
    inline = [
      "sudo dnf install -y ca-certificates tar gzip",
      "sudo install -d -m 0755 /etc/skiff /opt/skiff /var/log/skiff /var/lib/skiff/runner",
      "sudo tar -C /usr/local/bin -xzf /tmp/skiff.tar.gz ./skiff-runner ./skiff-worker",
      "sudo chmod 0755 /usr/local/bin/skiff-runner /usr/local/bin/skiff-worker",
      "sudo install -m 0644 /tmp/skiff-runner.service /etc/systemd/system/skiff-runner.service",
      "sudo install -m 0644 /tmp/collector.yaml /etc/skiff/collector.yaml",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable skiff-runner.service",
      "sudo /usr/local/bin/skiff-runner version --format json >/tmp/skiff-runner-version.json",
      "printf '%s\n' '{\"skiff\":{\"env\":\"smoke\",\"service\":\"smoke-api\",\"provider\":\"aws\",\"region\":\"${var.region}\",\"state_bucket\":\"file:///var/lib/skiff/smoke-state\",\"control_key\":\"services/smoke-api/control.json\"}}' | sudo tee /etc/skiff/runner.json >/dev/null",
      "sudo /usr/local/bin/skiff-runner config show --user-data /etc/skiff/runner.json --format json >/tmp/skiff-runner-config-smoke.json",
      "sudo rm -f /etc/skiff/runner.json",
      "sudo systemd-analyze verify /etc/systemd/system/skiff-runner.service"
    ]
  }

  post-processor "manifest" {
    output = "${var.artifact_dir}/runner-image-amd64-manifest.json"
    custom_data = {
      arch          = "amd64"
      ssm_arch      = "x86_64"
      version       = var.skiff_version
      channel       = var.channel
      ssm_prefix    = var.ssm_parameter_prefix
      ssm_parameter = "${var.ssm_parameter_prefix}/x86_64/${var.channel}"
    }
  }
}

build {
  name    = "runner-arm64"
  sources = ["source.amazon-ebs.runner_arm64"]

  provisioner "file" {
    source      = "${var.artifact_dir}/skiff_${var.skiff_version}_linux_arm64.tar.gz"
    destination = "/tmp/skiff.tar.gz"
  }

  provisioner "file" {
    source      = "build/runner-image/systemd/skiff-runner.service"
    destination = "/tmp/skiff-runner.service"
  }

  provisioner "file" {
    source      = "build/runner-image/collector.yaml"
    destination = "/tmp/collector.yaml"
  }

  provisioner "shell" {
    inline_shebang = "/bin/bash -euxo pipefail"
    inline = [
      "sudo dnf install -y ca-certificates tar gzip",
      "sudo install -d -m 0755 /etc/skiff /opt/skiff /var/log/skiff /var/lib/skiff/runner",
      "sudo tar -C /usr/local/bin -xzf /tmp/skiff.tar.gz ./skiff-runner ./skiff-worker",
      "sudo chmod 0755 /usr/local/bin/skiff-runner /usr/local/bin/skiff-worker",
      "sudo install -m 0644 /tmp/skiff-runner.service /etc/systemd/system/skiff-runner.service",
      "sudo install -m 0644 /tmp/collector.yaml /etc/skiff/collector.yaml",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable skiff-runner.service",
      "sudo /usr/local/bin/skiff-runner version --format json >/tmp/skiff-runner-version.json",
      "printf '%s\n' '{\"skiff\":{\"env\":\"smoke\",\"service\":\"smoke-api\",\"provider\":\"aws\",\"region\":\"${var.region}\",\"state_bucket\":\"file:///var/lib/skiff/smoke-state\",\"control_key\":\"services/smoke-api/control.json\"}}' | sudo tee /etc/skiff/runner.json >/dev/null",
      "sudo /usr/local/bin/skiff-runner config show --user-data /etc/skiff/runner.json --format json >/tmp/skiff-runner-config-smoke.json",
      "sudo rm -f /etc/skiff/runner.json",
      "sudo systemd-analyze verify /etc/systemd/system/skiff-runner.service"
    ]
  }

  post-processor "manifest" {
    output = "${var.artifact_dir}/runner-image-arm64-manifest.json"
    custom_data = {
      arch          = "arm64"
      ssm_arch      = "arm64"
      version       = var.skiff_version
      channel       = var.channel
      ssm_prefix    = var.ssm_parameter_prefix
      ssm_parameter = "${var.ssm_parameter_prefix}/arm64/${var.channel}"
    }
  }
}
