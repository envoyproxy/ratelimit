package main

import (
	"github.com/envoyproxy/ratelimit/src/service_cmd/runner"
	"github.com/envoyproxy/ratelimit/src/settings"
)

func main() {
	runner := runner.NewRunner(settings.NewSettings())
	runner.Run()
}
