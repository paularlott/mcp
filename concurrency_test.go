package mcp

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentToolCalls(t *testing.T) {
	s := NewServer("s", "1")
	s.RegisterTool(NewTool("add", "", Number("a", ""), Number("b", "")), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		a, _ := req.Float("a")
		b, _ := req.Float("b")
		return NewToolResponseText(fmt.Sprintf("%.0f", a+b)), nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			resp, err := s.CallTool(context.Background(), "add", map[string]any{"a": float64(n), "b": 1.0})
			if err != nil {
				errs <- err
				return
			}
			if resp.Content[0].Text != fmt.Sprintf("%d", n+1) {
				errs <- fmt.Errorf("bad sum")
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrency error: %v", e)
	}
}
