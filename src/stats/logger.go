package stats

import (
	"strconv"
	"time"

	logger "github.com/sirupsen/logrus"
)

const logTemplate = `{"level":"debug","ts":%[1]v,"logger":"gostats.loggingsink","msg":"flushing %[2]v","json":{"name":"%[3]v","type":"%[2]v","value":"%[4]v"}}`

// LoggingSink is a gostats.FlushableSink that logs all stats using
// the rate limit logger.
type LoggingSink struct{}

func (s *LoggingSink) log(name, typ string, value float64) {
	if logger.IsLevelEnabled(logger.DebugLevel) {
		sec := float64(time.Now().UnixNano()) / float64(time.Second)
		logger.Debugf(logTemplate, strconv.FormatFloat(sec, 'f', 6, 64), typ, name, value)
	}
}

// Implementation of the gostats.FlushableSink interface

func (s *LoggingSink) FlushCounter(name string, value uint64) { s.log(name, "counter", float64(value)) }
func (s *LoggingSink) FlushGauge(name string, value uint64)   { s.log(name, "gauge", float64(value)) }
func (s *LoggingSink) FlushTimer(name string, value float64)  { s.log(name, "timer", value) }
func (s *LoggingSink) Flush()                                 { s.log("", "all stats", 0) }
