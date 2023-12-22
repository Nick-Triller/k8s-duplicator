# k8s-duplicator

![Build](https://github.com/Nick-Triller/k8s-duplicator/actions/workflows/ci.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/Nick-Triller/k8s-duplicator)](https://goreportcard.com/report/github.com/Nick-Triller/k8s-duplicator)

k8s-duplicator is a kubernetes controller that duplicates secrets from one to
all other namespaces and keeps them in sync.

## Motivation

This controller can be used to sync any secret into all namespaces by adding the
annotation `duplicator.k8s.nicktriller.com/duplicate: "true"` to the secret.

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
helm repo add k8s-duplicator https://nicktriller.github.io/k8s-duplicator/
helm repo update
helm upgrade --install -n k8s-duplicator --create-namespace k8s-duplicator k8s-duplicator/k8s-duplicator
```

## Usage

Add the annotation `duplicator.k8s.nicktriller.com/duplicate: "true"` to a secret to duplicate it to all namespaces.
The copies will be kept in sync with the original secret.
Copies are deleted if the original secret is deleted or the `duplicate` annotation is removed from the original secret.
Copies have the same name as the original secret.
If a namespace already contains a secret with the same name as the original secret, the controller will not overwrite it.

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

The copies are annotated with an annotation that references the original secret:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: another-namespace
  annotations:
    duplicator.k8s.nicktriller.com/source: "some-namespace/my-secret"
stringData:
  foo: bar
```

## Release process

Push a git tag in the form of `docker-1.0.0` on `main` branch to execute unit and integration
tests and publish a docker image to https://hub.docker.com/r/nicktriller/k8s-duplicator.
The docker image will be tagged with the version given in the git tag.

Push a git tag in the form of `helm-1.0.0` on `main` branch to publish the helm chart.
The chart is packaged and pushed into `gh_pages` branch with the version given in the git tag.
