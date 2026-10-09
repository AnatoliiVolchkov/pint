# Example configuration for linting vmalert rules against VictoriaMetrics.

parser {
  # Parse rule expressions as MetricsQL.
  # This allows WITH templates, MetricsQL functions, optional lookbehind windows
  # and all other MetricsQL syntax.
  query_language = "metricsql"
}

# Single-node VictoriaMetrics.
prometheus "vm" {
  uri     = "http://victoriametrics:8428"
  backend = "victoriametrics"

  # VictoriaMetrics doesn't expose scrape configuration, set the scrape interval
  # used by vmagent so checks like promql/rate can validate range selectors.
  scrapeInterval = "30s"

  # vmalert evaluating rules against this server. pint reads its -datasource.queryStep
  # and -external.label flags, and the rule files it loaded for `pint watch rule_files`.
  # It isn't queried with --offline.
  vmalertURI = "http://vmalert:8880"

  # vmalert -datasource.queryStep, set it if vmalert doesn't use the default 5m and
  # vmalertURI isn't set. group/interval uses it to find rules that can't see results
  # of other rules.
  queryStep = "5m"

  # External labels configured on vmalert via -external.label flags, needed with --offline.
  externalLabels = {
    cluster = "demo"
  }

  include = ["rules/single/.+"]
}

# Cluster VictoriaMetrics, queries are sent to vmselect for tenant 0.
prometheus "vm-cluster" {
  uri      = "http://vmselect-1:8481/select/0/prometheus"
  failover = ["http://vmselect-2:8481/select/0/prometheus"]
  backend  = "victoriametrics"
  include  = ["rules/cluster/.+"]

  # vmselect doesn't know the retention of vmstorage nodes, set their -retentionPeriod
  # so promql/range_query and promql/offset can check queries against it.
  retention = "1y"
}

rule {
  cost {
    # maxPeakSamples is not available on VictoriaMetrics. maxTotalSamples uses
    # samplesScanned from the query trace.
    maxSeries             = 10000
    maxTotalSamples       = 1000000
    maxEvaluationDuration = "30s"
  }
}
