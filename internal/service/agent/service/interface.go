package service

import (
	"context"

	llmModel "github.com/mechta-market/pulse_bot/internal/service/llm/model"
	pulseModel "github.com/mechta-market/pulse_bot/internal/service/pulse/model"
)

type llmI interface {
	Name() string
	Complete(ctx context.Context, req *llmModel.Request) (*llmModel.Response, error)
}

type pulseI interface {
	Catalog(ctx context.Context) (*pulseModel.Catalog, error)
	Call(ctx context.Context, name string, arguments string) (*pulseModel.CallResult, error)
}
