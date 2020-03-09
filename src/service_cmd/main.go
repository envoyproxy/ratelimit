package main

import "github.com/envoyproxy/ratelimit/src/service_cmd/runner"

func main() {
	runner := runner.NewRunner()
	runner.Run()
}
