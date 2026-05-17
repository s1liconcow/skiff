variable "region" {
  type    = string
  default = "us-west-2"
}

variable "skiff_version" {
  type = string
}

variable "artifact_dir" {
  type    = string
  default = "dist"
}

source "amazon-ebs" "runner" {
  region        = var.region
  instance_type = "t3.micro"
  ssh_username  = "ubuntu"

  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    owners      = ["099720109477"]
    most_recent = true
  }

  ami_name = "skiff-runner-${var.skiff_version}-{{timestamp}}"
  tags = {
    "skiff.dev/managed" = "true"
    "skiff.dev/role"    = "runner"
    "skiff.dev/version" = var.skiff_version
  }
}

build {
  sources = ["source.amazon-ebs.runner"]

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
    inline = [
      "set -eux",
      "sudo mkdir -p /etc/skiff /opt/skiff",
      "sudo tar -C /usr/local/bin -xzf /tmp/skiff.tar.gz ./skiff-runner ./skiff-worker",
      "sudo cp /tmp/skiff-runner.service /etc/systemd/system/skiff-runner.service",
      "sudo cp /tmp/collector.yaml /etc/skiff/collector.yaml",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable skiff-runner.service"
    ]
  }
}
