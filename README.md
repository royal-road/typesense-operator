<p align="left">
  <img src="banner.png" alt="TyKO Logo" />
</p>

[//]: # (# TyKO — Typesense Kubernetes Operator)

[//]: # (**Kubernetes-native automation for highly available Typesense clusters.**)

[![TypesenseSupportedVersions](https://img.shields.io/badge/Typesense-^26.0-green?logo=stackblitz)]() [![KubernetesSupportedVersions](https://img.shields.io/badge/Kubernetes-^1.26-green?logo=kubernetes&logoColor=f5f5f5)]()
[![License](https://img.shields.io/github/license/akyriako/typesense-operator)]() ![Go](https://img.shields.io/github/go-mod/go-version/akyriako/typesense-operator
) ![GitHub Release](https://img.shields.io/github/v/release/akyriako/typesense-operator) [![Stars](https://img.shields.io/github/stars/akyriako/typesense-operator?logo=github)]()

[//]: # (---)

## 🎯 Why TyKO?

**Simplify the deployment, scaling, and management of Typesense clusters in Kubernetes.**

- Deploy highly-available **Typesense** clusters with a **single declarative YAML manifest**
- Automates **Typesense** [lifecycle management](https://akyriako.github.io/typesense-operator-docs/docs/getting-started#key-features) (config maps, secrets, volumes, statefulsets, services, ingress or http routes, metrics, scrapers)
- Automates **Raft quorum configuration and discovery**, while preserving membership during uncertain health and leaving disaster recovery to a fenced, manual procedure
- Built with Go & Operator SDK — lightweight, Kubernetes-native, and flexible
- Community-driven, with plethora of examples for Kind, CCE, AKS, EKS, GCP, and more

## ⚡ Get Started

```bash
# Install the Operator
helm repo add tyko https://akyriako.github.io/typesense-operator/
helm repo update

helm upgrade --install typesense-operator tyko/typesense-operator -n typesense-system --create-namespace --reset-values
```

<details>
<summary>Quick example for Open Telekom Cloud CCE</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-otc-1
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    storageClassName: csi-disk
```
</details>

<details>
<summary>Quick example for Open Telekom Cloud CCE with OBS S3 support</summary>

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: otc-aksk
  namespace: default
  labels:
    secret.kubernetes.io/used-by: csi
type: cfe/secure-opaque
data:
  access.key: WE5WWVhVNU*****
  secret.key: Nnk4emJyZ0*****
---
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-otc-2
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    storageClassName: csi-obs
    accessMode: ReadWriteMany
    annotations:
      csi.storage.k8s.io/fstype: obsfs
      volume.beta.kubernetes.io/storage-provisioner: everest-csi-provisioner
      csi.storage.k8s.io/node-publish-secret-name: otc-aksk
      csi.storage.k8s.io/node-publish-secret-namespace: default
      everest.io/csi.volume-name-prefix: c-otc-2
```
</details>

<details>
<summary>Quick example for Open Telekom Cloud CCE with SFS Turbo S3 support</summary>

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: otc-aksk
  namespace: default
  labels:
    secret.kubernetes.io/used-by: csi
type: cfe/secure-opaque
data:
  access.key: WE5WWVhVNU*****
  secret.key: Nnk4emJyZ0*****
---
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-otc-3
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    storageClassName: csi-obs
    accessMode: ReadWriteMany
    annotations:
      everest.io/obs-volume-type: STANDARD
      csi.storage.k8s.io/fstype: s3fs
      volume.beta.kubernetes.io/storage-provisioner: everest-csi-provisioner
      csi.storage.k8s.io/node-publish-secret-name: otc-aksk
      csi.storage.k8s.io/node-publish-secret-namespace: default
      everest.io/csi.volume-name-prefix: c-otc-3
```
</details>

<details>
<summary>Quick example for bare metal K3s with Democratic CSI</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-bm-k3s
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    storageClassName: nfs
```
</details>

<details>
<summary>Quick example for Kind</summary>

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: typesense-local-path
provisioner: rancher.io/local-path
reclaimPolicy: Delete
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
---
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-kind
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    size: 150Mi
    storageClassName: typesense-local-path
```
</details>

<details>
<summary>Quick example for AWS EKS</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-aws
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    size: 100Mi
    storageClassName: gp2
```
</details>

<details>
<summary>Quick example for Azure AKS</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-azure
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    storageClassName: managed-csi
```
</details>

<details>
<summary>Quick example for GCP</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-gcp
spec:
  image: typesense/typesense:30.0
  replicas: 3
  storage:
    storageClassName: standard-rwo
```
</details>

You can find more examples and analytical installation instructions in the [Installation](https://akyriako.github.io/typesense-operator-docs/docs/installation/) and [Configuration](https://akyriako.github.io/typesense-operator-docs/docs/crds) guides.

## Quorum safety in this fork

This fork intentionally does **not** recover an existing cluster by automatically
scaling it down to a singleton and back up. A `/status` timeout, a leaderless
election, or multiple reported leaders does not establish which Raft history is
authoritative. Successful `/health` responses do not change that.

- Leaderless or failed status observations leave quorum not ready and are retried.
  Multiple leaders report an error requiring investigation and fenced recovery.
- Health or Raft-log errors do not cause the operator to purge running Pods.
  The operator does not delete PVCs or Raft data as a recovery action.
- Incomplete peer discovery preserves the existing nodes ConfigMap rather than
  reducing it to the subset of Pods or endpoints currently visible. Membership
  updates wait for complete discovery while normal reconciliation can still
  apply requested workload repairs or recreate a missing headless Service.
  Actual discovery API errors are reported rather than treated as success.
  Replacement Pod IPs are updated once the complete set is discovered.
- Initial deployment still creates the requested number of replicas, including
  explicitly requested single-node clusters. Ordinary requested scaling and
  membership updates remain supported; a health failure does not request scaling.
  Unschedulable Pending Pods may still be recreated without changing membership
  or deleting their PVCs. Kubernetes container restarts and normal StatefulSet
  rolling updates are unchanged.
- The existing `resetPeersOnError` server setting remains unchanged, as do the CRD
  fields `forceResetPeersConfigOnUpdate` and `incrementalQuorumRecovery`. None
  enables automatic singleton disaster recovery in this fork.

If quorum does not recover naturally, **fence client writes and competing
replicas before selecting or restoring any authoritative state**. Preserve all
replicas' data and backups, investigate the Raft histories, and follow a
Typesense-version-appropriate manual recovery procedure. Do not treat an arbitrary
reachable replica, a passing health check, or a temporary scale-down as proof of
authority. This patch cannot repair histories already diverged by earlier
recovery attempts; fence and assess such clusters before resuming reconciliation.

### Upgrading from operator 0.3.5

This patch is based on upstream 0.4.1, not 0.3.5, and is not a promise of a
non-disruptive operator-image-only upgrade:

- Upstream 0.4.1 changed the StatefulSet hash calculation and desired Pod template.
  Reconciliation can schedule a rolling replacement of otherwise healthy Pods.
- Default sidecar images changed: healthcheck moved from
  `akyriako78/typesense-healthcheck:0.1.8` to
  `quay.io/akyriako/typesense-healthcheck:0.2.0`, and metrics moved to
  `quay.io/akyriako/typesense-prometheus-exporter:0.1.9`. Existing explicit
  `spec.healthcheck.image` and `spec.metrics.image` values are respected.
- New upstream CRD fields include `storage.accessMode`, `securityContext`,
  `service`, `httpRoutes`, and `forceResetPeersConfigOnUpdate`. An old CRD does
  not supply their new defaults and may prune those fields. This patch defaults
  an absent storage access mode to `ReadWriteOnce` when rendering the workload,
  preserving compatibility with older valid CRs without changing their storage.
  Explicit `ReadWriteOnce` and `ReadWriteMany` values remain respected.
- Review CRDs, RBAC, pinned images, and the rendered StatefulSet before rollout.
  New Gateway API management needs its upstream permissions when that API is
  installed. Back up data and plan managed-Pod replacement separately from
  deployment of the operator.

The quorum safety changes add no CRD fields and do not change Typesense server
code or the on-disk storage layout. Upstream recovery documentation linked
elsewhere in this README does not override this fork's safety policy.

## 📚 Documentation

- [Getting Started](https://akyriako.github.io/typesense-operator-docs/docs/getting-started)
- [How it works](https://akyriako.github.io/typesense-operator-docs/docs/how-it-works)
- [Configuration & CRD Specs](https://akyriako.github.io/typesense-operator-docs/docs/crds)
- [Monitoring & Metrics](https://akyriako.github.io/typesense-operator-docs/docs/metrics)

## 💬 Community & Support

Join the conversation:
- **GitHub Discussions**: ask questions, share feedback, submit suggestions or help others
- **Issues**: report bugs or request features

## 📦 Project Status

TyKO is an **independently maintained** project (not affiliated with Typesense, Inc.).
- Latest version: **0.4.2**
- Tested on: Kubernetes 1.35 (earliest 1.26), Typesense 30.0 (earliest 26.0)
- Contributions welcome! See [FAQ](https://akyriako.github.io/typesense-operator-docs/docs/faq) and [Development](https://akyriako.github.io/typesense-operator-docs/docs/development)

## ⭐ Help us Grow
- Star the repo if you want this operator to grow
- Try TyKO in your own infrastructure and share your feedback or list yourselves as [Adopters](https://akyriako.github.io/typesense-operator-docs/docs/adopters)
- Spread the word: blog posts, tutorials, or community shoutouts help others discover it
