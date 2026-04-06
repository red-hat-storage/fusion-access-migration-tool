# Fusion Access → Fusion Data Foundation migration tool

## AI Generated codebase

Migrates an existing **Red Hat Fusion Access** deployment (IBM Spectrum Scale **6.0.0.2**) to **Fusion Data Foundation** (Spectrum Scale **6.0.1.0**).

## Version requirements (preflight)

Phase 1 **blocks** the migration unless:

| Requirement | Notes |
|-------------|--------|
| **OpenShift** | Cluster version must be **4.21.x** (matches `ClusterVersion` history; other minors are rejected). |
| **Catalog** | A `CatalogSource` in `openshift-marketplace` whose name contains `fusion` or `fdf` (FDF install path). |
| **Namespaces** | `ibm-spectrum-scale`, `ibm-spectrum-scale-operator`, and `ibm-fusion-access` must exist. |
| **FDF not already installed** | No `odf-operator` subscription in `openshift-storage` whose CSV provider is **IBM** (resume after a partial install requires `--continue`). |

The ODF subscription channel used for FDF is **`stable-4.21`**, aligned with the supported OCP minor.

Log in with a kubeconfig that can manage cluster-scoped and namespace-scoped resources (`oc login` / usual admin).

## How to build

```bash
make build    # produces ./migrate
# or
go build -o migrate .
```

## How to run

```bash
./migrate [OPTIONS]
```

Use **`./migrate -h`** or **`./migrate --help`** for the full phase list and flag text.

### Common options

| Flag | Purpose |
|------|--------|
| `--dry-run` / `-d` | Print what would happen; **no** cluster changes and **no** updates to the progress file. |
| `--continue` | **Skip Phase 1 preflight** and run phases 2–6 only. Use after a failure or when namespaces / FDF state no longer match a “fresh” install (e.g. `ibm-fusion-access` already removed). |
| `--state-file PATH` | JSON checkpoint path (default: **`.fusion-access-migration-progress`** in the current working directory). |

### Suggested flows

- **First-time check (strict preflight):**  
  `./migrate --dry-run`

- **Execute migration:**  
  `./migrate`

- **Resume without re-running preflight:**  
  `./migrate --continue`

- **Inspect completed vs pending phases (uses last non–dry-run checkpoint):**  
  `./migrate --dry-run --continue`


