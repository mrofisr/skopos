SELECT
    if(`ServiceName` != '', `ServiceName`, `Attributes`['service.name']) AS GroupName,
    count() AS SeriesCount,
    sum(if(sampled <= 0, 0, increase * ((sampled + startAdj + endAdj) / sampled) / {__range:Float64})) AS value
FROM (SELECT *,
    if(increase > 0 AND FirstV >= 0 AND sampled * FirstV / increase < if(dStart0 >= threshold, avg / 2, dStart0),
       sampled * FirstV / increase,
       if(dStart0 >= threshold, avg / 2, dStart0)) AS startAdj,
    if(dEnd0 >= threshold, avg / 2, dEnd0) AS endAdj
FROM (SELECT *,
    dateDiff('millisecond', FirstT, LastT) / 1000 AS sampled,
    (dateDiff('millisecond', FirstT, LastT) / 1000) / (N - 1) AS avg,
    ((dateDiff('millisecond', FirstT, LastT) / 1000) / (N - 1)) * 1.1 AS threshold,
    dateDiff('millisecond', {__start:DateTime64(9)}, FirstT) / 1000 AS dStart0,
    dateDiff('millisecond', LastT, {__end:DateTime64(9)}) / 1000 AS dEnd0,
    LastV - FirstV + ResetSum AS increase
FROM (SELECT
    `MetricName`, `ServiceName`, `Attributes`,
    min(`TimeUnix`) AS FirstT,
    argMin(`Value`, `TimeUnix`) AS FirstV,
    max(`TimeUnix`) AS LastT,
    argMax(`Value`, `TimeUnix`) AS LastV,
    count() AS N,
    sumIf(prev, `Value` < prev) AS ResetSum
FROM (SELECT
    `MetricName`, `ServiceName`, `Attributes`, `TimeUnix`, `Value`,
    lagInFrame(`Value`) OVER (PARTITION BY `MetricName`, `ServiceName`, `Attributes` ORDER BY `TimeUnix`) AS prev
FROM `otel`.`otel_metrics_sum` FINAL
WHERE `TimeUnix` > {__start:DateTime64(9)} AND `TimeUnix` <= {__end:DateTime64(9)}
  AND toStartOfFiveMinutes(`TimeUnix`) >= {__bucket_start:DateTime64(9)}
  AND toStartOfFiveMinutes(`TimeUnix`) <= {__bucket_end:DateTime64(9)}
  AND ((`MetricName` = {metric_filter:String})))
GROUP BY `MetricName`, `ServiceName`, `Attributes`)
WHERE N >= 2 AND FirstT < LastT))
GROUP BY GroupName