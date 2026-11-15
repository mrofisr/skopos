SELECT
    `MetricName`,
    `TimeUnix`,
    `ServiceName`,
    `Attributes`,
    `Value`
FROM `otel`.`otel_metrics_gauge` FINAL
WHERE (`TimeUnix` >= {__start:DateTime64(9)} AND `TimeUnix` <= {__end:DateTime64(9)})
    AND ((replaceRegexpAll(`MetricName`, {norm_pattern:String}, {norm_replacement:String}) = {metric_filter:String}) AND ((`ServiceName` = {service_filter_0:String}) OR (`ServiceName` = {service_empty_0:String})))
LIMIT {__limit:UInt64}
FORMAT JSON
