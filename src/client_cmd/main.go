package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/envoyproxy/ratelimit/src/utils"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	"go.opentelemetry.io/otel"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
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
	oltpProtocol := flag.String("oltp-protocol", "", "protocol to use when exporting tracing span, accept http, grpc or empty (disable tracing) as value, please use OLTP environment variables to set endpoint (refer to README.MD)")
	grpcServerTlsCACert := flag.String("grpc-server-ca-file", "", "path to the server CA file for TLS connection")
	grpcUseTLS := flag.Bool("grpc-use-tls", false, "Use TLS for connection to server")
	grpcTlsCertFile := flag.String("grpc-cert-file", "", "path to the client cert file for TLS connection")
	grpcTlsKeyFile := flag.String("grpc-key-file", "", "path to the client key for TLS connection")
	flag.Parse()

	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("Flag: --%s=%q\n", f.Name, f.Value)
	})

	if *oltpProtocol != "" {
		tp := InitTracerProvider(*oltpProtocol)
		defer func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				log.Printf("Error shutting down tracer provider: %v", err)
			}
		}()
	}
	dialOptions := []grpc.DialOption{
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	}
	if *grpcUseTLS {
		tlsConfig := utils.TlsConfigFromFiles(*grpcTlsCertFile, *grpcTlsKeyFile, *grpcServerTlsCACert, utils.ServerCA, false)
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		dialOptions = append(dialOptions, grpc.WithInsecure())
	}
	conn, err := grpc.Dial(*dialString, dialOptions...)
	if err != nil {
		fmt.Printf("error connecting: %s\n", err.Error())
		os.Exit(1)
	}

	defer conn.Close()
	c := pb.NewRateLimitServiceClient(conn)
	desc := make([]*pb_struct.RateLimitDescriptor, len(descriptorsValue.descriptors))
	for i, v := range descriptorsValue.descriptors {
		desc[i] = v
	}
	response, err := c.ShouldRateLimit(
		context.Background(),
		&pb.RateLimitRequest{
			Domain:      *domain,
			Descriptors: desc,
			HitsAddend:  1,
		})
	if err != nil {
		fmt.Printf("request error: %s\n", err.Error())
		os.Exit(1)
	}

	fmt.Printf("response: %s\n", response.String())
}

// using a simpler setup in this trace provider for simplicity
func InitTracerProvider(protocol string) *sdktrace.TracerProvider {
	var client otlptrace.Client

	switch protocol {
	case "http":
		client = otlptracehttp.NewClient()
	case "grpc":
		client = otlptracegrpc.NewClient()
	default:
		fmt.Printf("Invalid otlptrace client protocol: %s", protocol)
		panic("Invalid otlptrace client protocol")
	}

	exporter, err := otlptrace.New(context.Background(), client)
	if err != nil {
		log.Fatalf("creating OTLP trace exporter: %v", err)
	}

	resource := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("RateLimitClient"),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp
}
