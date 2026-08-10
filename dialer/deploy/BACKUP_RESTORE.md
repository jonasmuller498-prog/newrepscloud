# Backup and restore

The base creates a Longhorn RWO `postgres-backups` PVC and a logical backup
CronJob, but the CronJob has `suspend: true`. A second PVC in the same cluster
is not disaster recovery. Configure and test an external Longhorn backup target
before treating any snapshot or PVC as durable.

## Logical backups

Review database size, retention, free space, and the maintenance NetworkPolicy.
Then enable only the CronJob:

```bash
kubectl -n voice-dialer patch cronjob postgres-logical-backup \
  --type=merge -p '{"spec":{"suspend":false}}'
kubectl -n voice-dialer create job --from=cronjob/postgres-logical-backup \
  postgres-logical-backup-manual
kubectl -n voice-dialer logs job/postgres-logical-backup-manual
```

Each run creates a PostgreSQL custom-format dump atomically and removes dumps
older than 14 days. Copy dumps to encrypted off-cluster storage. Monitor failed
Jobs and PVC usage. Periodically run `pg_restore --list` against a copied dump
and perform a full restore drill in a disposable namespace.

To pause:

```bash
kubectl -n voice-dialer patch cronjob postgres-logical-backup \
  --type=merge -p '{"spec":{"suspend":true}}'
```

## Longhorn snapshots and backups

First discover the installed CSI snapshot class instead of assuming it:

```bash
kubectl get volumesnapshotclass
kubectl -n longhorn-system get settings.longhorn.io backup-target -o yaml
```

`optional/backups/volume-snapshot.yaml` uses the common
`longhorn-snapshot-vsc`; change it locally if the cluster uses another class.
It snapshots only the new `data-postgres-0` claim. A filesystem snapshot of a
running database is crash-consistent, not a substitute for a logical dump.
For stronger consistency, pause dialing/writes and request a PostgreSQL
checkpoint before snapshotting.

The optional package is not referenced by the base. The production-backups
overlay composes it with the production overlay so generated Secret references
stay consistent. Applying it immediately requests a snapshot and a new
`postgres-snapshot-restore` PVC, so preview it:

```bash
kubectl diff -k overlays/production-backups
kubectl apply -k overlays/production-backups
kubectl -n voice-dialer get volumesnapshot postgres-manual-snapshot -w
```

Configure a Longhorn recurring backup job for the new PostgreSQL volume through
the approved cluster process. Do not modify a shared recurring-job resource
from this deployment. Use unique snapshot names for subsequent captures rather
than updating the existing object.

## Logical restore: paused twice

The optional restore Job defaults to `spec.suspend: true`. Its command also
requires the exact confirmation
`ALLOW_DESTRUCTIVE_RESTORE=I_UNDERSTAND_DATA_WILL_BE_REPLACED` and rejects the
placeholder filename. Thus an accidental apply cannot restore data.

For an approved restore:

1. Set `DIALING_ENABLED=false`, `CPS=0`, and disable the trunk.
2. Pause backups, stop application writes, and take a final safety snapshot.
3. Verify the selected dump digest and restore it into a disposable PostgreSQL
   instance first.
4. Copy the restore Job to a one-use file, set the exact dump basename and
   confirmation value, then change `suspend` to `false`.
5. Apply that one-use file, watch logs, run integrity/opt-out checks, and delete
   the Job afterward.
6. Resume only after application migrations and reconciliation succeed.

Never commit the edited destructive Job.

## Snapshot-volume restore

`postgres-snapshot-restore` is a separate PVC sourced from the snapshot. It is
not wired into the live StatefulSet, intentionally. Mount it read-only in a
disposable PostgreSQL recovery pod or a cloned StatefulSet using compatible
PostgreSQL 17 binaries. Validate data and export a logical dump.

Do not patch the live StatefulSet claim name or delete `data-postgres-0` as a
shortcut. A PVC template cannot safely switch an existing ordinal. Use a new
claim/workload, validate it, and perform a planned cutover with rollback media
retained.
