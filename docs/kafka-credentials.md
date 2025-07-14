# Kafka Credentials Support

This document describes how to use Kubernetes secrets to securely provide Kafka credentials for external Kafka deployments.

## Overview

The milvus-operator now supports reading Kafka credentials from Kubernetes secrets, similar to how MinIO credentials are handled. This improves security by avoiding hardcoded credentials in the Milvus configuration.

## Usage

### 1. Create a Secret

Create a Kubernetes secret containing your Kafka credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: milvus-kafka-credentials
  namespace: milvus-system
type: Opaque
data:
  username: <base64-encoded-username>
  password: <base64-encoded-password>
```

Or use kubectl:

```bash
kubectl create secret generic milvus-kafka-credentials \
    --namespace=milvus-system \
    --from-literal=username=your-kafka-username \
    --from-literal=password=your-kafka-password
```

### 2. Configure Milvus

Reference the secret in your Milvus configuration:

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-milvus
  namespace: milvus-system
spec:
  config:
    kafka:
      securityProtocol: SASL_PLAINTEXT
      saslMechanisms: PLAIN
      # saslUsername and saslPassword will be automatically injected
  dependencies:
    msgStreamType: kafka
    kafka:
      external: true
      secretRef: "milvus-kafka-credentials"  # Reference to the secret
      brokerList:
        - "kafka-broker:9092"
```

### 3. How it Works

When the operator processes the Milvus configuration:

1. It checks if `dependencies.kafka.secretRef` is specified
2. If present, it reads the secret from the same namespace
3. It extracts the `username` and `password` keys from the secret
4. It automatically injects these values into the configuration as `kafka.saslUsername` and `kafka.saslPassword`

## Secret Format

The secret must contain the following keys:

- `username`: The Kafka SASL username
- `password`: The Kafka SASL password

## Security Benefits

- **No hardcoded credentials**: Credentials are stored securely in Kubernetes secrets
- **Namespace isolation**: Secrets are namespace-scoped
- **Access control**: Use Kubernetes RBAC to control access to secrets
- **Rotation**: Credentials can be rotated by updating the secret

## Example

See the complete example in [config/samples/milvus_kafka_external_secret.yaml](../config/samples/milvus_kafka_external_secret.yaml).

## Troubleshooting

### Common Issues

1. **Secret not found**: Ensure the secret exists in the same namespace as the Milvus instance
2. **Wrong keys**: Make sure the secret contains `username` and `password` keys
3. **Permissions**: Verify the operator has permission to read secrets in the namespace

### Debugging

Check the operator logs for any error messages related to secret reading:

```bash
kubectl logs -n milvus-system deployment/milvus-operator
```

Look for messages like:
```
get kafka secret error: secrets "milvus-kafka-credentials" not found
```
