package trace

import (
	"context"

	"github.com/google/uuid"
	logger "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

func Init(protocol string, serviceName string, serviceNamespace string, serviceInstanceId string) *sdktrace.TracerProvider {
	client := createClient(protocol)
	exporter, err := otlptrace.New(context.Background(), client)
	if err != nil {
		logger.Fatalf("creating OTLP trace exporter: %v", err)
	}

	var useServiceInstanceId string
	if serviceInstanceId == "" {
		intUuid, err := uuid.NewRandom()
		if err != nil {
			logger.Fatalf("generating random uuid for trace exporter: %v", err)
		}
		useServiceInstanceId = intUuid.String()
	} else {
		useServiceInstanceId = serviceInstanceId
	}

	resource := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
		semconv.ServiceNamespaceKey.String(serviceNamespace),
		semconv.ServiceInstanceIDKey.String(useServiceInstanceId),
	)

	if err != nil {
		logger.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	logger.Infof("TracerProvider initialized with following parameters: protocol: %s, serviceName: %s, serviceNamespace: %s, serviceInstanceId: %s", protocol, serviceName, serviceNamespace, useServiceInstanceId)
	return tp
}

func createClient(protocol string) (client otlptrace.Client) {
	// endpoint is implicitly set by env variables, refer to https://opentelemetry.io/docs/reference/specification/protocol/exporter/
	switch protocol {
	case "http", "":
		client = otlptracehttp.NewClient()
	case "grpc":
		client = otlptracegrpc.NewClient()
	default:
		logger.Fatalf("Invalid otlptrace client protocol: %s", protocol)
		panic("Invalid otlptrace client protocol")
	}
	return
}
