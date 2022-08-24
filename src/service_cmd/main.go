package main

import (
	"github.com/irlapp/rate-limiter/src/service_cmd/runner"
	"github.com/irlapp/rate-limiter/src/settings"
)

func main() {
	runner := runner.NewRunner(settings.NewSettings())
	runner.Run()
}
