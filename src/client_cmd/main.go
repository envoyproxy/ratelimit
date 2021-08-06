package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

type descriptorsValue struct {
	descriptors []*pb_struct.RateLimitDescriptor
}

func (this *descriptorsValue) Set(arg string) error {
	pairs := strings.Split(arg, ",")
	entries := make([]*pb_struct.RateLimitDescriptor_Entry, len(pairs))
	for i, pair := range pairs {
		parts := strings.Split(pair, "=")
		if len(parts) != 2 {
			return errors.New("invalid descriptor list")
		}
		entries[i] = &pb_struct.RateLimitDescriptor_Entry{Key: parts[0], Value: parts[1]}
	}
	this.descriptors = append(this.descriptors, &pb_struct.RateLimitDescriptor{Entries: entries})

	return nil
}

func (this *descriptorsValue) String() string {
	ret := ""
	for _, descriptor := range this.descriptors {
		tmp := ""
		for _, entry := range descriptor.Entries {
			tmp += fmt.Sprintf(" <key=%s, value=%s> ", entry.Key, entry.Value)
		}
		ret += fmt.Sprintf("[%s] ", tmp)
	}
	return ret
}

func main() {
	dialString := flag.String(
		"dial_string", "localhost:8081", "url of ratelimit server in <host>:<port> form")
	domain := flag.String("domain", "", "rate limit configuration domain to query")
	descriptorsValue := descriptorsValue{[]*pb_struct.RateLimitDescriptor{}}
	flag.Var(
		&descriptorsValue, "descriptors",
		"descriptor list to query in <key>=<value>,<key>=<value>,... form")
	hitsAddend := flag.Uint("hitsAddend", 1, "the number of hits a request adds to the matched limit")
	flag.Parse()

	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("Flag: --%s=%q\n", f.Name, f.Value)
	})

	conn, err := grpc.Dial(*dialString, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("error connecting: %s\n", err.Error())
		os.Exit(1)
	}

	defer conn.Close()
	c := pb.NewRateLimitServiceClient(conn)
	response, err := c.ShouldRateLimit(
		context.Background(),
		&pb.RateLimitRequest{
			Domain:      *domain,
			Descriptors: descriptorsValue.descriptors,
			HitsAddend:  uint32(*hitsAddend),
		})
	if err != nil {
		fmt.Printf("request error: %s\n", err.Error())
		os.Exit(1)
	}

	fmt.Printf("response: %s\n", response.String())
}
