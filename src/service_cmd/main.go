package main

import "github.com/lyft/ratelimit/src/service_cmd/runner"

func main() {
	runner := runner.NewRunner()
	runner.Run()
}
