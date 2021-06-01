package service

import (
	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v3/trace"
)

type RedisStats struct {
	connectionActive stats.Gauge
	connectionTotal  stats.Counter
	connectionClose  stats.Counter
}

func PoolTrace(ps *RedisStats) trace.PoolTrace {
	return trace.PoolTrace{
		ConnCreated: func(_ trace.PoolConnCreated) {
			ps.connectionTotal.Add(1)
			ps.connectionActive.Add(1)
		},
		ConnClosed: func(_ trace.PoolConnClosed) {
			ps.connectionActive.Sub(1)
			ps.connectionClose.Add(1)
		},
	}
}

func NewRedisStats(scope stats.Scope) RedisStats {
	ret := RedisStats{}
	ret.connectionActive = scope.NewGauge("cx_active")
	ret.connectionTotal = scope.NewCounter("cx_total")
	ret.connectionClose = scope.NewCounter("cx_local_close")
	return ret
}
