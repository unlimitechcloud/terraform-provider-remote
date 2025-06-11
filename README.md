# Terraform Remote Provider

A generic Terraform provider to proxy resource lifecycle operations (`create`, `read`, `update`, `delete`) through an AWS Lambda function. This allows defining custom resources powered by your own logic.

## 🔧 Use Cases

- Provisioning infrastructure via Lambda
- Managing external systems through APIs
- Custom automation of third-party services
- Lightweight serverless Terraform integrations

---

## ✅ Requirements

- Go 1.18+
- Terraform v0.13+
- AWS Lambda function handling resource lifecycle
- IAM permissions for invoking the Lambda function

---

## 🧠 How the Lambda Function Works

Your Lambda function will receive a JSON payload like:

```json
{
  "phase": "create",
  "args": {
    "name": "example",
    "type": "custom"
  },
  "state": {
    "result.some_key": "previous_value"
  },
  "planning": false
}
