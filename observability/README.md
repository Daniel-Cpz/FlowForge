# Local/demo configurations

See [definitions and operations](../docs/observability.md).
Pinned Prometheus 3.5.0, Grafana 12.1.1 and Collector 0.133.0 are used by the
optional profile and disposable harness. Worker DNS discovery is actually tested;
global snapshot panels use max rather than sum. No credentials/TSDB/Grafana DB
or trace dumps belong in Git. Grafana uses anonymous Viewer, not a real admin password.

Validate with promtool check config, Collector validate --config, JSON parsing
and scripts/test-phase9-harness.ps1. Runtime acceptance is scripts/phase9-failure.ps1;
measurement is scripts/phase9-benchmark.ps1. The experiment file requires generated
project/password/namespace variables, uses tmpfs and exact finally cleanup.
