# terraform-provider-remote

[![Terraform](https://img.shields.io/badge/Terraform-Provider-7B42BC?logo=terraform)](https://registry.terraform.io/providers/YOUR_ORG/remote/latest)

A **generic Terraform provider** that delegates every resource-lifecycle phase
(**create, read, update, delete, plan**) to an **AWS Lambda** (or any Invoke-compatible
function). Ideal when you need Terraform to orchestrate systems that do **not**
have a native provider—or when you want a single, serverless point of truth for
complex orchestration logic.

---

## ✨ Highlights

| Feature                                            | Notes                                                            |
| -------------------------------------------------- | ---------------------------------------------------------------- |
| 🔧 100 % customizable                              | Write your logic once in Lambda, reuse in every Terraform run.   |
| 🏷 Multiple Lambdas via **provider alias**         | Keep unrelated workflows isolated but under one provider binary. |
| 🔄 Passes `args`, prior `state`, & `planning` flag | Perfect for idempotence & dry-run introspection.                 |
| 🔑 Strictly typed Lambda payload contract          | Fail fast and debug with clarity.                                |

---

## ⚡ Installation

```hcl
terraform {
  required_providers {
    remote = {
      source  = "YOUR_ORG/remote"
      version = ">= 0.1.0"
    }
  }
}
```

---

## ⚙️ Usage (Single Function)

```hcl
provider "remote" {
  lambda_arn = "arn:aws:lambda:us-east-1:123456789012:function:ec2LifecycleHandler"
}

resource "remote_resource" "example" {
  args = {
    instance_type = "t3.micro"
    image_id      = "ami-12345678"
  }
}
```

---

## ✉️ Usage (Multiple Lambdas via Alias)

```hcl
provider "remote" {
  alias      = "ec2"
  lambda_arn = "arn:aws:lambda:us-east-1:123456789012:function:ec2Handler"
}

provider "remote" {
  alias      = "dns"
  lambda_arn = "arn:aws:lambda:us-east-1:123456789012:function:dnsHandler"
}

resource "remote_resource" "ec2" {
  provider = remote.ec2
  args = {
    name = "web"
    size = "t3.small"
  }
}

resource "remote_resource" "dns" {
  provider = remote.dns
  args = {
    hostname = "web.example.com"
    address  = "10.0.0.1"
  }
}
```

---

## 🚀 Lambda Contract

The Lambda function receives a **JSON** payload:

```json
{
  "phase": "create" | "read" | "update" | "delete",
  "args": { ... },
  "state": { ... },
  "planning": true | false
}
```

### Expected Response:

```json
{
  "id": "unique-id-string",
  "result": {
    "any": "json-compatible object",
    "you": "need"
  }
}
```

* `id`: must be a non-empty string for all phases except `delete`.
* `result`: will be available in Terraform via `remote_resource.result`.

If `id` is empty on `read`, the resource is considered **destroyed**.

---

## 👾 Example Lambda

### Node.js (JavaScript)

```js
exports.handler = async (event) => {
  const { phase, args, state, planning } = event;

  if (phase === "create") {
    const id = `instance-${Math.floor(Math.random() * 100000)}`;
    return { id, result: { instance_status: "started" } };
  }

  if (phase === "read") {
    return { id: state.id, result: { instance_status: "running" } };
  }

  if (phase === "delete") {
    return { id: "" };
  }
};
```

### Go

```go
func handler(ctx context.Context, e Event) (Response, error) {
  switch e.Phase {
  case "create":
    return Response{ID: "res-abc", Result: map[string]interface{}{"status": "done"}}, nil
  case "read":
    return Response{ID: e.State["id"].(string), Result: map[string]interface{}{"ok": true}}, nil
  case "delete":
    return Response{ID: ""}, nil
  default:
    return Response{}, fmt.Errorf("unhandled phase: %s", e.Phase)
  }
}
```

---

## 🔎 Debugging

Set environment variables to increase verbosity:

```bash
export TF_LOG=TRACE
export TF_IN_AUTOMATION=1
```

---

## 🌐 Publishing to Terraform Registry

1. Repo name must follow the pattern: `terraform-provider-remote`
2. Release a GitHub tag: `v0.1.0`
3. Authenticate to Terraform Registry via GitHub
4. Push and verify: [https://registry.terraform.io/providers](https://registry.terraform.io/providers)

See: [https://developer.hashicorp.com/terraform/plugins/distribution](https://developer.hashicorp.com/terraform/plugins/distribution)

---

## ✅ TODOs

* Add `Import` support
* Add retry config and timeout override
* Improve `result` typing via `Schema` injection

---

## ✨ License

MIT
