package floorplan

import (
	"fmt"
	"os"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/pool"
)

// ModelFromEnv is the model LLM_BASE_URL, LLM_API_KEY and LLM_MODEL describe (any
// OpenAI-compatible API that takes images), nil when none is set.
func ModelFromEnv() (domain.Generator, error) {
	key := os.Getenv("LLM_API_KEY")
	if key == "" {
		return nil, nil
	}
	p, err := pool.NewClient("llm", os.Getenv("LLM_BASE_URL"), key, os.Getenv("LLM_MODEL"))
	if err != nil {
		return nil, fmt.Errorf("模型配置有误：%w", err)
	}
	return p, nil
}
