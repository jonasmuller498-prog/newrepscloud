# Backup and restore

The base creates a Longhorn RWO `postgres-backups` PVC and a logical backup
CronJob, but the CronJob has `suspend: true`. A second PVC in the same cluster
is not disaster recovery. Configure and test an external Longhorn backup target
before treating any PVC as durable. `dialer-safety-status` and the CronJob
annotation make the current configuration state visible; production dialing
validation requires an acknowledged external destination. Store only a
non-secret target identifier in `safety.env`; never put a signed URL or
credentials in that generated ConfigMap.

## Logical backups

Review database size, retention, free space, and the maintenance NetworkPolicy.
The backup uses the non-superuser runtime account. Configure encrypted export
from the backup PVC or a reviewed Longhorn recurring backup to the acknowledged
destination. Test restore first, update `safety.env`, then enable only the
CronJob:

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

No `VolumeSnapshot` or `VolumeSnapshotClass` is shipped here. Snapshot classes
are cluster-scoped operational inputs and cannot be guessed safely. Discover
the installed class and Longhorn backup target:

```bash
kubectl get volumesnapshotclass
kubectl -n longhorn-system get settings.longhorn.io backup-target -o yaml
```

Create any snapshot as a separately reviewed one-use manifest containing the
exact discovered class. A filesystem snapshot of a running database is only
crash-consistent. Pause dialing/writes and request a PostgreSQL checkpoint
first. Never add a guessed default class to this overlay.

## ARI journal consistency

`/media/ari-journal` is on the `dialer-media` Longhorn PVC. It can contain a
durable, redacted event whose PostgreSQL commit was interrupted or uncertain,
including an opt-out. Back up the media PVC as well as PostgreSQL, and restore
both from a coordinated recovery point while dialing and the trunk are
disabled. Restoring PostgreSQL without the matching journal can lose an
unprojected opt-out; restoring a newer journal against an older database can
correctly block readiness when its attempt does not exist.

Never delete or edit a pending or malformed journal record merely to make the
pod ready. Preserve a copy, determine the matching attempt and database state,
then follow an approved reconciliation procedure. Replayed records retain the
original deduplication key, so a commit that succeeded before a crash remains
idempotent.

Configure a Longhorn recurring backup job for the new PostgreSQL volume through
the approved cluster process. Do not modify a shared recurring-job resource
from this deployment. Use unique snapshot names for subsequent captures rather
than updating the existing object.

## Logical restore: paused twice

`overlays/production-backups` adds only the optional logical restore Job and
keeps generated runtime Secret references consistent. The Job defaults to
`spec.suspend: true`. Its command also
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

Create a separate PVC from a reviewed snapshot only after supplying the exact
live class and snapshot name. Do not wire it into the live StatefulSet. Mount
it read-only in a disposable PostgreSQL recovery pod or cloned StatefulSet
using compatible PostgreSQL 17 binaries. Validate data and export a logical
dump.

Do not patch the live StatefulSet claim name or delete `data-postgres-0` as a
shortcut. A PVC template cannot safely switch an existing ordinal. Use a new
claim/workload, validate it, and perform a planned cutover with rollback media
retained.
