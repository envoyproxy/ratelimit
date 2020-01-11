package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/api/v2/ratelimit"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

type requestValue struct {
	request *pb.RateLimitRequest
}

func (this *requestValue) Set(arg string) error {
	descriptor := pb_struct.RateLimitDescriptor{}

	pairs := strings.Split(arg, ",")
	for _, pair := range pairs {
		parts := strings.Split(pair, "=")
		if len(parts) != 2 {
			return errors.New("invalid descriptor list")
		}

		descriptor.Entries = append(
			descriptor.Entries,
			&pb_struct.RateLimitDescriptor_Entry{
				Key: parts[0], 
				Value: parts[1],
			},
		)
	}

	this.request.Descriptors = append(
		this.request.Descriptors,
		&descriptor,
	)

	return nil
}

func (this *requestValue) String() string {
	return this.request.String()
}

func main() {
	dialString := flag.String(
		"dial_string", 
		"localhost:8081", 
		"url of ratelimit server in <host>:<port> form")
	
	domain := flag.String(
		"domain", 
		"", 
		"rate limit configuration domain to query")

	hitsAddend := flag.Uint(
		"hits_addend",
		1,
		"amount to add to the cache on each hit",
	)
	
	myRequest := requestValue{&pb.RateLimitRequest{}}
	flag.Var(
		&myRequest, 
		"descriptors",
		"descriptor list to query in <key>=<value>,<key>=<value>,... form")
	flag.Parse()

	myRequest.request.Domain = *domain
	myRequest.request.HitsAddend = uint32(*hitsAddend)

	fmt.Printf("dial string: %s\n", *dialString)
	fmt.Printf("request: %v\n", myRequest.request)

	conn, err := grpc.Dial(*dialString, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("error connecting: %s\n", err.Error())
		os.Exit(1)
	}

	defer conn.Close()
	c := pb.NewRateLimitServiceClient(conn)

	response, err := c.ShouldRateLimit(
		context.Background(),
		myRequest.request)
	if err != nil {
		fmt.Printf("request error: %s\n", err.Error())
		os.Exit(1)
	}

	fmt.Printf("response: %s\n", response.String())
}
