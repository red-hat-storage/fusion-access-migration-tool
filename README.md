# Fusion Access → Fusion Data Foundation migration tool

## AI Generated codebase

Migrates an existing **Red Hat Fusion Access** deployment (IBM Spectrum Scale **6.0.0.2**) to **Fusion Data Foundation** (Spectrum Scale **6.0.1.0**).

## Version requirements (preflight)

Preflight (step 1) **blocks** the migration unless:

| Requirement | Notes |
|-------------|--------|
| **OpenShift** | Cluster version must be **4.21.x** (matches `ClusterVersion` history; other minors are rejected). |
| **Namespaces** | `ibm-spectrum-scale`, `ibm-spectrum-scale-operator`, and `ibm-fusion-access` must exist. |
| **IBM entitlement** | Secret **`ibm-entitlement-key`** must exist in **`ibm-fusion-access`**; the tool copies it to **`ibm-spectrum-scale`** immediately before deleting the Fusion Access namespace (migrate KMM step). |
| **ODF/FDF in openshift-storage** | If an `odf-operator` subscription exists, `status.currentCSV` must be set. Its CSV must have provider **IBM** and **`spec.version` 4.20.x** for a new migration. **4.21.x** is only accepted when **resuming** from checkpoint (after install phase may have upgraded FDF). If there is no `odf-operator` subscription, preflight continues (FDF not installed yet). |

The install phase reconciles the `odf-operator` Subscription to channel **`stable-4.21`** and catalog **`isf-data-foundation-catalog`** (clusters on IBM FDF **4.20.x** get an in-place Subscription update). After reconcile, expect IBM FDF on the **4.21.x** line.

The migration binary is intended to run in-cluster inside a Kubernetes Job using a ServiceAccount with required RBAC.
`deploy/resources.yaml` scopes namespaced permissions through RoleBindings for the namespaces touched by migration and uses a separate ClusterRole only for required cluster-scoped APIs.
It also creates `fusion-access-migration` and `openshift-kmm` namespaces so RBAC objects can be applied before the Job in `deploy/migration-job.yaml` starts.

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
| `FDF_CATALOG_IMAGE` | Yes (unless dry-run) | Container image for the `isf-data-foundation-catalog` CatalogSource applied before FDF install. |

### Checkpoint and restart behavior

There is no `--continue` flag or other resume switch; the Job resumes only using migration state in ConfigMap key `migration-progress.json`.

- Migration state is stored in ConfigMap key `migration-progress.json`.
- After each successful phase, `lastCompletedPhase` is updated.
- On Job restart, migration resumes from `lastCompletedPhase + 1`.
- If `lastCompletedPhase > 0`, preflight is skipped automatically.
- On final success, state is marked `completed`.

### Failure policy

- Retryable failures exit with code `1` so the Job retries (`restartPolicy: OnFailure`).
- Fatal validation/preflight failures exit with code `42`.
- The sample Job manifest includes `podFailurePolicy` to fail the Job immediately on exit code `42`.

### Dry-run flow

- Run dry run with `MIGRATION_DRY_RUN=true`.
- Dry run does not update checkpoint state.
- Run actual migration as a new Job execution with `MIGRATION_DRY_RUN=false`.

