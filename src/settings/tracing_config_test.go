package settings

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func unsetTracingAndOtelEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TRACING_ENABLED",
		"TRACING_EXPORTER_PROTOCOL",
		"TRACING_SERVICE_NAME",
		"TRACING_SERVICE_NAMESPACE",
		"TRACING_SERVICE_INSTANCE_ID",
		"TRACING_SAMPLING_RATE",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_SERVICE_NAME",
		"OTEL_TRACES_EXPORTER",
		"OTEL_TRACES_SAMPLER",
		"OTEL_TRACES_SAMPLER_ARG",
		"OTEL_RESOURCE_ATTRIBUTES",
		"OTEL_SDK_DISABLED",
	} {
		os.Unsetenv(key)
	}
}

func TestOtelTracingFallbacks_Protocol(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.Equal(t, "grpc", settings.TracingExporterProtocol)
}

func TestOtelTracingFallbacks_ProtocolHttpProtobuf(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.Equal(t, "http", settings.TracingExporterProtocol)
}

func TestOtelTracingFallbacks_ServiceName(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("OTEL_SERVICE_NAME", "my-ratelimit")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.Equal(t, "my-ratelimit", settings.TracingServiceName)
}

func TestOtelTracingFallbacks_ResourceAttributes(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.namespace=prod,service.instance.id=pod-123")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.Equal(t, "prod", settings.TracingServiceNamespace)
	assert.Equal(t, "pod-123", settings.TracingServiceInstanceId)
}

func TestOtelTracingFallbacks_EnabledFromTracesExporter(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.True(t, settings.TracingEnabled)
}

func TestOtelTracingFallbacks_DisabledFromSdkDisabled(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	os.Setenv("OTEL_SDK_DISABLED", "true")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.False(t, settings.TracingEnabled)
}

func TestOtelTracingFallbacks_SamplingRate(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")
	os.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.Equal(t, 0.25, settings.TracingSamplingRate)
}

func TestTracingEnvOverridesOtelEnv(t *testing.T) {
	unsetTracingAndOtelEnv(t)
	os.Setenv("TRACING_EXPORTER_PROTOCOL", "http")
	os.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	os.Setenv("TRACING_SERVICE_NAME", "custom-service")
	os.Setenv("OTEL_SERVICE_NAME", "otel-service")
	defer unsetTracingAndOtelEnv(t)

	settings := NewSettings()

	assert.Equal(t, "http", settings.TracingExporterProtocol)
	assert.Equal(t, "custom-service", settings.TracingServiceName)
}
