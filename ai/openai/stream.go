package openai

import (
	"context"
)

// ChatStream provides an iterator interface for streaming chat completion responses.
// It is designed to be used in a for loop pattern:
//
//	stream := openai.NewChatStream(ctx, responseChan, errorChan)
//	for stream.Next() {
//	    chunk := stream.Current()
//	    // process chunk
//	}
//	if err := stream.Err(); err != nil {
//	    // handle error
//	}
type ChatStream struct {
	responseChan <-chan ChatCompletionResponse
	errorChan    <-chan error
	ctx          context.Context
	current      *ChatCompletionResponse
	err          error
	done         bool
}

// NewChatStream creates a new ChatStream from response and error channels.
func NewChatStream(ctx context.Context, responseChan <-chan ChatCompletionResponse, errorChan <-chan error) *ChatStream {
	return &ChatStream{
		responseChan: responseChan,
		errorChan:    errorChan,
		ctx:          ctx,
	}
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
