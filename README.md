# k8s-duplicator

k8s-duplicator is a kubernetes controller that duplicates secrets from one to
all other namespaces and keeps them in sync.

## Motivation

This controller can be used to make any secret available in all namespaces by adding an annotation to the secret:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: some-namespace
  annotations:
    duplicator.k8s.nicktriller.com/duplicate: "true"
stringData:
  foo: bar
```

My specific use case for this controller is provisioning a wildcard certificate
with cert-manager and then making the certicate available in all namespaces as described in the
[cert manager docs](https://cert-manager.io/docs/devops-tips/syncing-secrets-across-namespaces/):
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: wildcard-k8s-nicktriller-com
  namespace: kube-system
spec:
  dnsNames:
  - '*.k8s.nicktriller.com'
  issuerRef:
    group: cert-manager.io
    kind: ClusterIssuer
    name: letsencrypt-prod
  secretName: wildcard-k8s-nicktriller-com
  secretTemplate:
    annotations:
      # Annotation that instructs k8s-duplicator to duplicate the certificate secret to all namespaces
      duplicator.k8s.nicktriller.com/duplicate: "true"
```

[kubernetes-reflector](https://github.com/emberstack/kubernetes-reflector)
is a similar project with more features that can be used to achieve the same goal,
but it had reliability problems - it would randomly stop reconciling secrets until it was restarted.
Also, implementing my own controller seemed like a fun project.

## Install

```bash
helm repo add k8s-duplicator https://nicktriller.github.io/k8s-duplicator
helm repo update
helm upgrade --install -n k8s-duplicator k8s-duplicator k8s-duplicator/k8s-duplicator
```
