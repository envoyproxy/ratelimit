package main

import (
	"github.com/zackzhangverkada/ratelimit/src/service_cmd/runner"
	"github.com/zackzhangverkada/ratelimit/src/settings"
)

func main() {
	runner := runner.NewRunner(settings.NewSettings())
	runner.Run()
}
