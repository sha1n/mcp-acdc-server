# Running Ledger on Kubernetes

The Helm chart is the supported path. It renders a StatefulSet, a headless
Service, and a PersistentVolumeClaim per replica.

## Helm chart

Add the repository and install the chart into a namespace of your choosing.
Chart values mirror `ledger.yaml` one for one, so anything expressible in the
configuration file is expressible in values.

## Probes

The chart wires two HTTP probes. Both are cheap and neither touches storage.

### Readiness

The readiness endpoint reports whether the replica has acquired its data
directory lock and replayed its tail. A replica that is still replaying answers
`503`, which keeps it out of the Service until replay finishes.

### Liveness

The liveness endpoint answers as long as the process can schedule work. It
deliberately ignores replay progress, so a slow replay never restarts a healthy
replica.

## Rolling updates

Roll one replica at a time and wait for readiness between steps. The chart sets
this policy by default.
