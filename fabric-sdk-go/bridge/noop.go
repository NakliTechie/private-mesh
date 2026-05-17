package bridge

import (
	"context"
	"fmt"
)

// NoopAdapterName is the well-known adapter name conformance tests use to
// exercise the bridge-call dispatch path without hitting an external service.
// Hub deployments register a NoopAdapter under this name so test 12/13 of
// the conformance suite can verify "caveat passes" without depending on
// real credentials or network reach.
const NoopAdapterName = "conformance-test"

// NoopAdapter is an inert adapter — it accepts any operation, doesn't call
// out to the network, and echoes the input as the result. Safe to register
// in any deployment; intended for the conformance suite and for tools that
// want a dry-run target.
type NoopAdapter struct{}

func (NoopAdapter) Name() string    { return NoopAdapterName }
func (NoopAdapter) Version() string { return "1.0.0" }

func (NoopAdapter) Operations() []OperationSpec {
	return []OperationSpec{
		{
			Name:        "echo",
			Description: "Echoes the supplied params as result.echo.",
			Params: []ParamSpec{
				{Name: "message", Type: "string"},
			},
		},
		{
			Name:        "transfer",
			Description: "Inert transfer used by conformance tests for max-amount / only-domain caveat checks.",
			Params: []ParamSpec{
				{Name: "amount", Type: "integer"},
				{Name: "currency", Type: "string"},
				{Name: "domain", Type: "string"},
			},
			SideEffects: false,
		},
		{
			Name:        "fetch",
			Description: "Inert fetch — conformance only.",
		},
	}
}

func (NoopAdapter) Call(ctx context.Context, req *CallRequest) (*CallResponse, error) {
	// Honor cancellation — the conformance runner asserts on this.
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("conformance-test: context: %w", ctx.Err())
	default:
	}
	switch req.Operation {
	case "echo", "transfer", "fetch":
		// fine
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownOperation, req.Operation)
	}
	return &CallResponse{
		Result: map[string]any{
			"adapter":   NoopAdapterName,
			"operation": req.Operation,
			"echo":      req.Params,
		},
		Metrics: CallMetrics{
			DurationMs: 1,
		},
	}, nil
}

