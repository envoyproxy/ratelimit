package settings

import (
	"os"
	"strconv"
	"strings"
)

func ApplyOtelTracingFallbacks(s *Settings) {
	if _, ok := os.LookupEnv("TRACING_ENABLED"); !ok {
		if enabled, ok := otelTracingEnabledFromEnv(); ok {
			s.TracingEnabled = enabled
		}
	}

	if _, ok := os.LookupEnv("TRACING_EXPORTER_PROTOCOL"); !ok {
		if protocol, ok := otelExporterProtocolFromEnv(); ok {
			s.TracingExporterProtocol = protocol
		}
	}

	if _, ok := os.LookupEnv("TRACING_SERVICE_NAME"); !ok {
		if serviceName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")); serviceName != "" {
			s.TracingServiceName = serviceName
		}
	}

	resourceAttributes := otelResourceAttributesFromEnv()
	if _, ok := os.LookupEnv("TRACING_SERVICE_NAMESPACE"); !ok {
		if namespace := resourceAttributes["service.namespace"]; namespace != "" {
			s.TracingServiceNamespace = namespace
		}
	}
	if _, ok := os.LookupEnv("TRACING_SERVICE_INSTANCE_ID"); !ok {
		if instanceID := resourceAttributes["service.instance.id"]; instanceID != "" {
			s.TracingServiceInstanceId = instanceID
		}
	}

	if _, ok := os.LookupEnv("TRACING_SAMPLING_RATE"); !ok {
		if samplingRate, ok := otelSamplingRateFromEnv(); ok {
			s.TracingSamplingRate = samplingRate
		}
	}
}

func otelTracingEnabledFromEnv() (bool, bool) {
	if sdkDisabled := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED"))); sdkDisabled == "true" {
		return false, true
	}

	if tracesExporter, ok := os.LookupEnv("OTEL_TRACES_EXPORTER"); ok {
		return strings.ToLower(strings.TrimSpace(tracesExporter)) != "none", true
	}

	return false, false
}

func otelExporterProtocolFromEnv() (string, bool) {
	protocol := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	if protocol == "" {
		return "", false
	}

	switch protocol {
	case "grpc":
		return "grpc", true
	case "http", "http/protobuf", "http/protobuf+json":
		return "http", true
	default:
		return protocol, true
	}
}

func otelResourceAttributesFromEnv() map[string]string {
	attributes := make(map[string]string)
	for _, pair := range strings.Split(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		attributes[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return attributes
}

func otelSamplingRateFromEnv() (float64, bool) {
	sampler := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")))
	switch sampler {
	case "always_on":
		return 1, true
	case "always_off":
		return 0, true
	case "traceidratio", "parentbased_traceidratio":
		arg := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))
		if arg == "" {
			return 0, false
		}
		rate, err := strconv.ParseFloat(arg, 64)
		if err != nil {
			return 0, false
		}
		return rate, true
	default:
		return 0, false
	}
}
