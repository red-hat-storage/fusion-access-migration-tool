# Fusion Access → Fusion Data Foundation migration tool

## AI Generated codebase

Migrates an existing **Red Hat Fusion Access** deployment (IBM Spectrum Scale **6.0.0.2**) to **Fusion Data Foundation** (Spectrum Scale **6.0.1.0**).

## Version requirements (preflight)

Preflight (step 1) **blocks** the migration unless:

| Requirement | Notes |
|-------------|--------|
| **OpenShift** | Cluster version must be **4.21.x** (matches `ClusterVersion` history; other minors are rejected). |
| **Namespaces** | `ibm-spectrum-scale`, `ibm-spectrum-scale-operator`, and `ibm-fusion-access` must exist. |
| **Cluster health** | `ClusterVersion` must not report `Failing=True` or `Degraded=True`. |
| **Spectrum Scale** | At least one `clusters.scale.spectrum.ibm.com` must exist. Any `filesystems` present must report healthy/mounted, and any `localdisks` present must be Ready. |
| **Secure boot signing** | Secrets `secureboot-signing-key` and `secureboot-signing-key-pub` in `ibm-fusion-access` must either both exist (secure-boot cluster) or both be absent. Exactly one is rejected. |
| **ODF/FDF in openshift-storage** | If an `odf-operator` subscription exists, `status.currentCSV` must be set. Its CSV must have provider **IBM** at `spec.version` **4.20.x** (**4.21.x** only when **resuming** from checkpoint, since the install phase may already have upgraded it), or provider **Red Hat** at **4.20.x / 4.21.x**, which is migrated to IBM FDF during the install phase. If there is no `odf-operator` subscription, preflight continues (FDF not installed yet). |
| **FDF catalog is newer than installed Red Hat ODF** | When Red Hat ODF is installed, the `odf-operator` version offered by `FDF_CATALOG_IMAGE` on channel **`stable-4.21`** must be **strictly above** the installed version. See below. |

> **Note:** `ibm-entitlement-key` is *not* checked by preflight. It is read from `ibm-fusion-access` during the uninstall step and copied to `ibm-spectrum-scale` so image pulls keep working after the Fusion Access namespace is deleted.

The install phase reconciles the `odf-operator` Subscription to channel **`stable-4.21`** and catalog **`isf-data-foundation-catalog`** (clusters on IBM FDF **4.20.x** get an in-place Subscription update). After reconcile, expect IBM FDF on the **4.21.x** line.

### Red Hat ODF must be older than the FDF catalog

OLM resolves operator upgrades by CSV name, and Red Hat and IBM builds share the `odf-operator.vX.Y.Z` naming scheme. If the FDF catalog offers a version that is **lower than or equal to** the installed Red Hat ODF — for example installed ODF **4.21.15** against a GA'd FDF of **4.21.13** — OLM issues no InstallPlan, the Red Hat CSVs are never replaced, and the cluster silently stays on Red Hat ODF.

Preflight therefore refuses to start in that case. The tool does **not** force the swap by deleting CSVs, so the remedy is to migrate once an FDF release above the installed ODF version is available in the catalog.

This rules out the version-ordering cause only. OLM also needs an upgrade edge (`replaces` / `skips` / `skipRange`) from the installed CSV, which cannot be determined from version numbers, so **passing preflight does not guarantee the swap will happen**. The install phase therefore also requires the resulting `odf-operator` CSV to report provider **IBM**, not merely phase `Succeeded` — that assertion is what ultimately catches a migration that did not take effect.

Only the `odf-operator` package is version-checked, although the install phase repoints every Subscription in `openshift-storage` to the FDF catalog. ODF ships the whole operator family at a single z-version, so `odf-operator` is used as a proxy for all of them.

To read the catalog's version, preflight creates or updates the `isf-data-foundation-catalog` CatalogSource from `FDF_CATALOG_IMAGE`, waits for it to report READY against the new image, and reads the `odf-operator` PackageManifest. It writes only to that CatalogSource and touches nothing in Fusion Access — but note it is the one preflight check that writes to the cluster, that it overwrites the spec of an existing `isf-data-foundation-catalog`, and that it can take several minutes. It runs last, after every read-only check, and is skipped entirely when the installed ODF is already IBM or when no ODF is installed.

Under `MIGRATION_DRY_RUN=true` the CatalogSource is not created, so this check warns that it cannot verify the version and continues.

The migration binary is intended to run in-cluster inside a Kubernetes Job using a ServiceAccount with required RBAC.
`deploy/resources.yaml` scopes namespaced permissions through RoleBindings for the namespaces touched by migration and uses a separate ClusterRole only for required cluster-scoped APIs.
It also creates the `fusion-access-migration`, `openshift-kmm`, `openshift-storage` and `ibm-spectrum-fusion-ns` namespaces so RBAC objects can be applied before the Job in `deploy/migration-job.yaml` starts.

## How to build

```bash
make build    # produces ./migrate
# or
go build -o migrate ./cmd/migrate
```

## Build container image

```bash
make image TAG=v0.1.0
# with registry prefix
make image REGISTRY=quay.io/<org> TAG=v0.1.0
```

Push image:

```bash
make image-push REGISTRY=quay.io/<org> TAG=v0.1.0
```

Optional multi-arch build (buildx):

```bash
make image-buildx REGISTRY=quay.io/<org> TAG=v0.1.0
```

The runtime image is minimal (distroless, non-root) and contains only the `migrate` binary.

## How to run as a Job

Apply cluster resources (namespaces, RBAC), then the Job:

```bash
kubectl apply -f deploy/resources.yaml
kubectl apply -f deploy/migration-job.yaml
```

Update `deploy/migration-job.yaml` and set:

```yaml
image: quay.io/<org>/fusion-access-migration-tool:v0.1.0
```

Or update via make using the same image variables:

```bash
# update image in deploy/migration-job.yaml
make job-image-manifest REGISTRY=quay.io/<org> TAG=v0.1.0
```

The container writes logs to stdout; use:

```bash
kubectl logs -n fusion-access-migration job/fusion-access-migration -f
```

### Environment variables

| Env var | Required | Purpose |
|---------|----------|---------|
| `MIGRATION_DRY_RUN` | No | `true`/`false`. Dry run never updates migration state ConfigMap. |
| `MIGRATION_STATE_CONFIGMAP_NAMESPACE` | Yes | Namespace of the progress ConfigMap. |
| `MIGRATION_STATE_CONFIGMAP_NAME` | Yes | Name of the progress ConfigMap used for phase checkpointing. |
| `FDF_CATALOG_IMAGE` | Yes (unless dry-run) | Container image for the `isf-data-foundation-catalog` CatalogSource. Applied in preflight (to read the offered `odf-operator` version) and again before FDF install. |
| `FUSION_OPERATOR_CATALOG_IMAGE` | Yes (unless dry-run) | Container image for the `ibm-operator-catalog` CatalogSource used when installing Fusion Operator (phase 7). |

### Checkpoint and restart behavior

There is no `--continue` flag or other resume switch; the Job resumes only using migration state in ConfigMap key `migration-progress.json`.

- Migration state is stored in ConfigMap key `migration-progress.json`.
- After each successful phase, `lastCompletedPhase` is updated.
- On Job restart, migration resumes from `lastCompletedPhase + 1`.
- If `lastCompletedPhase > 0`, preflight is skipped automatically.
- On final success, state is marked `completed`.
- If `lastCompletedPhase` has reached `7` or the status is already `completed`, the Job logs that there is nothing to do and exits `0` without running any phase.

### Failure policy

- Preflight (phase 1) failures exit with code `42`. Invalid environment configuration and in-cluster client initialisation failures also exit `42`.
- Every other phase failure exits with code `1`, however fatal it is.
- The Job uses `restartPolicy: Never` with `backoffLimit: 20`, so an exit code `1` is retried by a new pod that resumes from the checkpoint.
- The sample Job manifest includes `podFailurePolicy` to fail the Job immediately on exit code `42` rather than retrying.

### Dry-run flow

- Run dry run with `MIGRATION_DRY_RUN=true`.
- Dry run does not update checkpoint state, and **ignores any existing checkpoint**: it always runs all phases from phase 1.
- Dry run creates nothing, so the FDF catalog version check cannot run and only warns. Use a real run to verify that the catalog can replace an installed Red Hat ODF.
- Run actual migration as a new Job execution with `MIGRATION_DRY_RUN=false`.

