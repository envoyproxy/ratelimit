package trace

import (
	"context"
	"sync"

	"github.com/google/uuid"
	logger "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

var (
	testSpanExporter   *tracetest.InMemoryExporter
	testSpanExporterMu sync.Mutex
)

func InitProductionTraceProvider(protocol string, serviceName string, serviceNamespace string, serviceInstanceId string, samplingRate float64) *sdktrace.TracerProvider {
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
	// trace if parent contains root span and is sampled
	// otherwise only trace according to sampling rate
	// if samplingRate >= 1, the AlwaysSample sampler is used
	// if samplingRate <= 0, the NeverSampler sampler is used
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplingRate))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	logger.Infof("TracerProvider initialized with following parameters: protocol: %s, serviceName: %s, serviceNamespace: %s, serviceInstanceId: %s, samplingRate: %f",
		protocol, serviceName, serviceNamespace, useServiceInstanceId, samplingRate)
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

// This function returns the initialized inMemoryExporter if it already exists. If not, it initializes an inMemoryExporter, a trace provider using the exporter, and bind otel package with the trace provider. It is designed to serve testing purpose solely.
// Note: only call this function once in each of the test packages, and assign the returned exporter to a package level variable
func GetTestSpanExporter() *tracetest.InMemoryExporter {
	testSpanExporterMu.Lock()
	defer testSpanExporterMu.Unlock()
	if testSpanExporter != nil {
		return testSpanExporter
	}
	// init a new InMemoryExporter and share it with the entire test runtime
	testSpanExporter := tracetest.NewInMemoryExporter()

	// add in-memory span exporter to default openTelemetry trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		// use syncer instead of batcher here to leverage its synchronization nature to avoid flaky test
		sdktrace.WithSyncer(testSpanExporter),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return testSpanExporter
}
