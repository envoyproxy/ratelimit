package stats

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

// loggingSink is a gostats.FlushableSink that logs all stats using
// the rate limit logger.
type loggingSink struct {
	now func() time.Time
}

// NewLoggingSink logs the gostats metrics using the rate limit logger.
// See the gostats.NewLoggingSink function for more details.
func NewLoggingSink() gostats.FlushableSink {
	return &loggingSink{now: time.Now}
}

type sixDecimalPlacesFloat float64

func (f sixDecimalPlacesFloat) MarshalJSON() ([]byte, error) {
	var ret []byte
	ret = strconv.AppendFloat(ret, float64(f), 'f', 6, 64)
	return ret, nil
}

type logLine struct {
	Level     string                `json:"level"`
	Timestamp sixDecimalPlacesFloat `json:"ts"`
	Logger    string                `json:"logger"`
	Message   string                `json:"msg"`
	JSON      map[string]string     `json:"json"`
}

func (s *loggingSink) log(name, typ string, value float64) {
	if !logger.IsLevelEnabled(logger.DebugLevel) {
		return
	}

	nanos := s.now().UnixNano()
	sec := sixDecimalPlacesFloat(float64(nanos) / float64(time.Second))
	kv := map[string]string{
		"type":  typ,
		"value": fmt.Sprintf("%f", value),
	}
	if name != "" {
		kv["name"] = name
	}

	b, err := json.Marshal(logLine{
		Message:   fmt.Sprintf("flushing %s", typ),
		Level:     "debug",
		Timestamp: sec,
		Logger:    "gostats.loggingsink",
		JSON:      kv,
	})
	if err == nil {
		logger.Debug(string(b))
	}
}

func (s *loggingSink) FlushCounter(name string, value uint64) { s.log(name, "counter", float64(value)) }
func (s *loggingSink) FlushGauge(name string, value uint64)   { s.log(name, "gauge", float64(value)) }
func (s *loggingSink) FlushTimer(name string, value float64)  { s.log(name, "timer", value) }
func (s *loggingSink) Flush()                                 { s.log("", "all stats", 0) }
