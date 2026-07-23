package main

import (
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func noopTracer() trace.Tracer {
	return noop.NewTracerProvider().Tracer("test")
}
