package main

import (
	"context"

	hweb "hometwin/internal/web"
)

// knowledge lets the assistant ask the home's knowledge graph.
type knowledge struct{ srv *hweb.Server }

func (k knowledge) Ask(ctx context.Context, q string) (string, []string, error) {
	a, err := k.srv.Ask(ctx, q)
	return a.Text, a.Nodes, err
}
