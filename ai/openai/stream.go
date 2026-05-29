package openai

import (
	"context"
	"sync"
)

type ChatStream struct {
	responseChan <-chan ChatCompletionResponse
	errorChan    <-chan error
	ctx          context.Context
	current      *ChatCompletionResponse
	err          error
	done         bool

	retryOnce   sync.Once
	retryMeta   *RetryMetadata
	retrySignal chan struct{}
}

// NewChatStream creates a new ChatStream from response and error channels.
func NewChatStream(ctx context.Context, responseChan <-chan ChatCompletionResponse, errorChan <-chan error) *ChatStream {
	return &ChatStream{
		responseChan: responseChan,
		errorChan:    errorChan,
		ctx:          ctx,
		retrySignal:  make(chan struct{}),
	}
}

func (s *ChatStream) SetRetryMetadata(meta *RetryMetadata) {
	s.retryOnce.Do(func() {
		s.retryMeta = meta
		close(s.retrySignal)
	})
}

func (s *ChatStream) Retry() *RetryMetadata {
	<-s.retrySignal
	return s.retryMeta
}

// Next advances to the next response chunk.
// Returns true if a chunk is available, false if the stream is done or an error occurred.
func (s *ChatStream) Next() bool {
	if s.done {
		return false
	}

	for {
		select {
		case <-s.ctx.Done():
			s.err = s.ctx.Err()
			s.done = true
			return false
		case err, ok := <-s.errorChan:
			if ok && err != nil {
				s.err = err
				s.done = true
				return false
			}
			// Channel closed with no error — nil it out so we drain
			// any remaining buffered responses before stopping.
			s.errorChan = nil
			continue
		case resp, ok := <-s.responseChan:
			if !ok {
				s.done = true
				return false
			}
			s.current = &resp
			return true
		}
	}
}

// Current returns the current response chunk.
// Must be called after Next returns true.
func (s *ChatStream) Current() ChatCompletionResponse {
	if s.current == nil {
		return ChatCompletionResponse{}
	}
	return *s.current
}

// Err returns any error that occurred during streaming.
// Should be checked after Next returns false.
func (s *ChatStream) Err() error {
	return s.err
}

// Done returns true if the stream has completed.
func (s *ChatStream) Done() bool {
	return s.done
}
