package main

import "github.com/replicon/ratelimit/src/service_cmd/runner"

func main() {
	runner := runner.NewRunner()
	runner.Run()
}
