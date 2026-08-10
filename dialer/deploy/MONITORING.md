# Optional Prometheus monitoring

The base includes a private ClusterIP metrics Service but default-deny blocks
all scrapes. It does not install custom resources. Use the optional package only
when these CRDs already exist:

```bash
kubectl get crd servicemonitors.monitoring.coreos.com
kubectl get crd prometheusrules.monitoring.coreos.com
kubectl diff -k optional/monitoring
kubectl apply -k optional/monitoring
```

The supplied NetworkPolicy expects Rancher Monitoring in
`cattle-monitoring-system` and Prometheus pods with
`app.kubernetes.io/name=prometheus`. Verify those selectors before apply.
Metrics listen only on the app's `9090` listener; never add that Service to the
Ingress or a NodePort.

The app's `/metrics` contract must include:

- `dialer_scheduler_enabled` gauge (`0` paused, `1` enabled);
- `dialer_slot_mismatch` gauge (`0` when active/reserved/configured reconcile);
- `dialer_cps_throttled_total` counter;
- `dialer_sip_attempts_total{result="accepted|failed"}` counter;
- `dialer_opt_out_persistence_failures_total` counter; and
- `dialer_ari_events_rejected_total` counter.

The rules alert on engine pod down, a long scheduler pause, slot mismatch,
persistent CPS throttling, SIP failure ratio over 20 percent after at least 20
attempts, and any opt-out persistence failure. A paused scheduler warning is
expected during initial commissioning; route or silence it deliberately rather
than deleting the guard.
Any rejected state-changing ARI event also alerts because it can indicate an
unexpected channel source or incompatible Asterisk event shape.

Check that the Prometheus Operator selects the ServiceMonitor's labels and that
scraped series receive a `namespace="voice-dialer"` target label. If the local
Operator uses different relabeling, adjust the rule matchers before apply.
