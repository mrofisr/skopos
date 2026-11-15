SELECT
    `MetricName`,
    `ServiceName`,
    `Attributes`,
    groupArray(`TimeUnix`) AS `Times`,
    groupArray(`Value`) AS `Values`
FROM `otel`.`otel_metrics_gauge` FINAL
WHERE (`TimeUnix` >= {__start:DateTime64(9)} AND `TimeUnix` <= {__end:DateTime64(9)})
    AND (replaceRegexpAll(`MetricName`, {norm_pattern:String}, {norm_replacement:String}) = {metric_filter:String})
GROUP BY `MetricName`, `ServiceName`, `Attributes`
LIMIT {__limit:UInt64}
FORMAT JSONCompact
