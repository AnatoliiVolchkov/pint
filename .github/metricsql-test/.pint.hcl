# Pint config for vmalert rules on VictoriaMetrics, enforcing the alerting standard:
#
# groups:
#   - name: <group-name>
#     interval: 30s
#     rules:
#       - alert: <AlertName>
#         expr: <metricsql expression>
#         for: <duration>
#         labels:
#           severity: critical            # critical | high | warning
#           team_name: "Example Team"
#           tags: "Alpha,Beta,Gamma"
#         annotations:
#           actions: "RestartService,ClearCache"
#           summary: "<one-line message>"
#           description: "<detailed explanation>"
#           grafana_url: "{{ $externalURL }}/d/<dashboard-id>/?viewPanel=<panel>&var-instance=..."
#           runbook_url: "<link to runbook>"   # required only for severity: critical
#           logs_url: "<link to logs>"
#
# vmalert-only fields are allowed and validated the way vmalert validates them:
#   groups: type, eval_offset, eval_delay, eval_alignment, concurrency, params,
#           headers, notifier_headers, debug
#   rules:  debug, update_entries_limit
# query_offset is reported, vmalert doesn't support it.
#
# Run from the root of the pint repository, see README.md in this directory:
#   make build
#   export VM_AUTH=$(printf 'user:password' | base64)
#   ./pint -c .github/metricsql-test/.pint.hcl lint .github/metricsql-test/rules .github/metricsql-test/k8s
#   ./pint -c .github/metricsql-test/.pint.hcl --offline lint .github/metricsql-test/rules .github/metricsql-test/k8s

parser {
  query_language = "metricsql"
  # Kubernetes manifests (ConfigMap, VMRule, PrometheusRule) are not plain rule files,
  # relaxed mode finds the rules inside them, including YAML embedded in ConfigMap data.
  # The pattern matches whether pint runs from the repository root or from this directory.
  relaxed = ["(.*/)?k8s/.*"]
}

ci {
  baseBranch = "main"
}

prometheus "vm" {
  uri            = "https://prom.example.com/prometheus"
  backend        = "victoriametrics"
  headers        = { "Authorization" = "Basic ${ENV_VM_AUTH}" }
  timeout        = "60s"
  concurrency    = 2
  rateLimit      = 5
  uptime         = "vm_app_version"
  scrapeInterval = "10s"
  required       = true
}

# Fields required on every alert.
rule {
  match {
    kind = "alerting"
  }

  name "[A-Z][A-Za-z0-9]+" {
    severity = "warning"
    comment  = "Alert names must use PascalCase, for example `HostOutOfMemory`."
  }

  for {
    min      = "1m"
    max      = "1h"
    severity = "warning"
  }

  label "severity" {
    severity = "bug"
    required = true
    values   = ["critical", "high", "warning"]
  }

  label "team_name" {
    severity = "bug"
    required = true
    value    = ".+"
  }

  label "tags" {
    severity = "bug"
    required = true
    value    = "[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*"
  }

  annotation "actions" {
    severity = "bug"
    required = true
    value    = "[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*"
  }

  annotation "summary" {
    severity = "bug"
    required = true
    value    = ".+"
  }

  annotation "description" {
    severity = "bug"
    required = true
    value    = ".+"
  }

  annotation "grafana_url" {
    severity = "bug"
    required = true
    value    = "(https?://|\\{\\{ *\\$externalURL *\\}\\}/).+"
  }

  annotation "logs_url" {
    severity = "bug"
    required = true
    value    = "https?://.+"
  }

  # Optional for high and warning alerts, but must be a link when it is set.
  annotation "runbook_url" {
    severity = "bug"
    required = false
    value    = "https?://.+"
  }
}

# runbook_url is required only for critical alerts.
rule {
  match {
    kind = "alerting"
    label "severity" {
      value = "critical"
    }
  }

  annotation "runbook_url" {
    severity = "bug"
    required = true
    comment  = "Critical alerts must link to a runbook."
  }
}
