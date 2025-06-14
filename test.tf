terraform {
  required_providers {
    remote = {
      source  = "unlimitechcloud/remote"
      version = "0.0.0"
    }
    cloudinit = {
      source = "hashicorp/cloudinit"
    }
  }
}

provider "remote" {
  alias  = "ec2"
  lambda = "CoderEc2WorkspaceHandler"
  region = "us-east-1"
}

resource "remote_resource" "ec2_instance" {
  provider = remote.ec2
  args = [
    jsonencode({
      spot               = false
      ami_id             = "ami-020cba7c55df1f615"
      instance_type      = "t3.micro"
      subnet_id          = "subnet-083224448dace4c25"
      security_group_ids = ["sg-08695b63da890ed3a"]
      key_pair_name      = "coder"
    }),
    "user data problem sasa",
    jsonencode({
      block_devices = {
        root = {
          volume_type = "gp3"
          volume_size = 60
        }
        workspace = {
          volume_type = "gp3"
          volume_size = 50
        }
      }

      tags = {
        # Name              = "coder-myws1"
        # Coder_Provisioned = "true"
      }

      coder = {
        workspace = {
          access_port       = 8080
          access_url        = "https://dev.coder.example.com/myws1"
          id                = "myws2"
          is_prebuild       = false
          is_prebuild_claim = false
          name              = "myws2"
          owner = {
            email             = "alice@example.com"
            full_name         = "Alice Example"
            groups            = ["developers"]
            id                = "user-1234"
            login_type        = "password"
            name              = "alice"
            oidc_access_token = ""
            rbac_roles = [
              {
                name   = "developer"
                org_id = "org-001"
              }
            ]
            session_token   = "SESSION_TOKEN_SAMPLE"
            ssh_private_key = "PRIVATE_KEY_SAMPLE"
            ssh_public_key  = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7..."
          }
          prebuild_count   = 0
          start_count      = 1
          template_id      = "template-5678"
          template_name    = "ubuntu-dev"
          template_version = "1.0.0"
          transition       = "start"
        }
      }
    })
  ]
}

output "my_object_json" {
  value = jsonencode(remote_resource.ec2_instance.result)
}
