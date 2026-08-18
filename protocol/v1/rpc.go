package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const (
	methodMetadata = "metadata"
	methodValidate = "validate"
	methodGenerate = "generate"
	methodScaffold = "scaffold"
	methodRead     = "read"
	methodCreate   = "create"
	methodUpdate   = "update"
	methodDelete   = "delete"
	methodDiff     = "diff"
	methodCheck    = "check"
)

type wireRequest struct {
	Protocol int             `json:"protocol"`
	ID       uint64          `json:"id"`
	Method   string          `json:"method"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type wireResponse struct {
	Protocol int             `json:"protocol"`
	ID       uint64          `json:"id"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Error    *RemoteError    `json:"error,omitempty"`
}

// RemoteError is an operation failure returned by the plugin process.
type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// Serve runs a protocol-v1 request loop until the host closes stdin. Plugins
// must reserve stdout for this function; logs belong on stderr.
func Serve(in io.Reader, out io.Writer, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("protocol v1: nil handler")
	}
	decoder := json.NewDecoder(in)
	writer := bufio.NewWriter(out)
	encoder := json.NewEncoder(writer)
	for {
		var request wireRequest
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("protocol v1: decode request: %w", err)
		}

		response := wireResponse{Protocol: Version, ID: request.ID}
		payload, err := dispatch(context.Background(), handler, request)
		if err != nil {
			var remote *RemoteError
			if errors.As(err, &remote) {
				response.Error = remote
			} else {
				response.Error = &RemoteError{Code: "operation_failed", Message: err.Error()}
			}
		} else if payload != nil {
			response.Payload, err = json.Marshal(payload)
			if err != nil {
				response.Error = &RemoteError{Code: "encoding_failed", Message: err.Error()}
			}
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("protocol v1: encode response: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("protocol v1: flush response: %w", err)
		}
	}
}

func dispatch(ctx context.Context, handler Handler, request wireRequest) (any, error) {
	if request.Protocol != Version {
		return nil, &RemoteError{
			Code:    "protocol_mismatch",
			Message: fmt.Sprintf("host requested protocol %d; plugin supports protocol %d", request.Protocol, Version),
		}
	}
	switch request.Method {
	case methodMetadata:
		return handler.Metadata(ctx)
	case methodValidate:
		implementation, ok := handler.(Validator)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input ValidateRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return implementation.Validate(ctx, &input)
	case methodGenerate:
		implementation, ok := handler.(Generator)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input GenerateRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return implementation.Generate(ctx, &input)
	case methodScaffold:
		implementation, ok := handler.(Scaffolder)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input ScaffoldRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return implementation.Scaffold(ctx, &input)
	case methodRead:
		implementation, ok := handler.(PlatformProvider)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input ReadRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return implementation.Read(ctx, &input)
	case methodCreate:
		implementation, ok := handler.(PlatformProvider)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input CreateRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return implementation.Create(ctx, &input)
	case methodUpdate:
		implementation, ok := handler.(PlatformProvider)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input UpdateRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return nil, implementation.Update(ctx, &input)
	case methodDelete:
		implementation, ok := handler.(PlatformProvider)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input DeleteRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return nil, implementation.Delete(ctx, &input)
	case methodDiff:
		implementation, ok := handler.(PlatformProvider)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input DiffRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return implementation.Diff(ctx, &input)
	case methodCheck:
		implementation, ok := handler.(Checker)
		if !ok {
			return nil, unsupported(request.Method)
		}
		var input CheckRequest
		if err := decodePayload(request.Payload, &input); err != nil {
			return nil, err
		}
		return implementation.Check(ctx, &input)
	default:
		return nil, unsupported(request.Method)
	}
}

func decodePayload(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return &RemoteError{Code: "invalid_request", Message: "request payload is missing"}
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return &RemoteError{Code: "invalid_request", Message: err.Error()}
	}
	return nil
}

func unsupported(method string) error {
	return &RemoteError{Code: "unsupported_method", Message: fmt.Sprintf("plugin does not implement %q", method)}
}

// Client owns one plugin subprocess. Calls are serialized because the v1 wire
// protocol preserves request order; cancellation terminates the process so a
// stuck plugin cannot leave the host blocked indefinitely.
type Client struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	decoder  *json.Decoder
	encoder  *json.Encoder
	stderr   *boundedBuffer
	cancel   context.CancelFunc
	nextID   uint64
	closed   bool
	metadata Metadata
}

// Start launches executable with the "serve" argument and performs the
// mandatory metadata handshake.
func Start(ctx context.Context, executable string, args ...string) (*Client, error) {
	commandArgs := append([]string{"serve"}, args...)
	return startCommand(ctx, executable, commandArgs)
}

func startCommand(ctx context.Context, executable string, commandArgs []string) (*Client, error) {
	lifetime, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(lifetime, executable, commandArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start plugin %s: stdin: %w", executable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start plugin %s: stdout: %w", executable, err)
	}
	stderr := &boundedBuffer{limit: 32 * 1024}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start plugin %s: %w", executable, err)
	}

	client := &Client{
		cmd:     cmd,
		stdin:   stdin,
		decoder: json.NewDecoder(stdout),
		encoder: json.NewEncoder(stdin),
		stderr:  stderr,
		cancel:  cancel,
	}
	var metadata Metadata
	if err := client.call(ctx, methodMetadata, nil, &metadata); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("plugin %s handshake failed: %w", executable, err)
	}
	if metadata.Protocol != Version {
		_ = client.Close()
		return nil, fmt.Errorf("plugin %s protocol mismatch: plugin=%d host=%d", executable, metadata.Protocol, Version)
	}
	if metadata.Source == "" || metadata.Version == "" || len(metadata.Kinds) == 0 {
		_ = client.Close()
		return nil, fmt.Errorf("plugin %s returned incomplete metadata", executable)
	}
	client.metadata = metadata
	return client, nil
}

func (c *Client) Metadata() Metadata { return c.metadata }

func (c *Client) Validate(ctx context.Context, request *ValidateRequest) (*ValidateResponse, error) {
	var response ValidateResponse
	if err := c.call(ctx, methodValidate, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Generate(ctx context.Context, request *GenerateRequest) (*GenerateResponse, error) {
	var response GenerateResponse
	if err := c.call(ctx, methodGenerate, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Scaffold(ctx context.Context, request *ScaffoldRequest) (*ScaffoldResponse, error) {
	var response ScaffoldResponse
	if err := c.call(ctx, methodScaffold, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Read(ctx context.Context, request *ReadRequest) (*ReadResponse, error) {
	var response ReadResponse
	if err := c.call(ctx, methodRead, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Create(ctx context.Context, request *CreateRequest) (*CreateResponse, error) {
	var response CreateResponse
	if err := c.call(ctx, methodCreate, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Update(ctx context.Context, request *UpdateRequest) error {
	return c.call(ctx, methodUpdate, request, nil)
}

func (c *Client) Delete(ctx context.Context, request *DeleteRequest) error {
	return c.call(ctx, methodDelete, request, nil)
}

func (c *Client) Diff(ctx context.Context, request *DiffRequest) (*DiffResponse, error) {
	var response DiffResponse
	if err := c.call(ctx, methodDiff, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Check(ctx context.Context, request *CheckRequest) (*CheckResponse, error) {
	var response CheckResponse
	if err := c.call(ctx, methodCheck, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) call(ctx context.Context, method string, input, output any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("plugin process is closed")
	}
	c.nextID++
	request := wireRequest{Protocol: Version, ID: c.nextID, Method: method}
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", method, err)
		}
		request.Payload = payload
	}
	if err := c.encoder.Encode(request); err != nil {
		return c.processError(fmt.Errorf("send %s request: %w", method, err))
	}

	type result struct {
		response wireResponse
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		var response wireResponse
		err := c.decoder.Decode(&response)
		completed <- result{response: response, err: err}
	}()

	var received result
	select {
	case <-ctx.Done():
		c.cancel()
		received = <-completed
		return fmt.Errorf("plugin %s canceled: %w", method, ctx.Err())
	case received = <-completed:
	}
	if received.err != nil {
		return c.processError(fmt.Errorf("receive %s response: %w", method, received.err))
	}
	response := received.response
	if response.Protocol != Version {
		return fmt.Errorf("plugin response protocol mismatch: plugin=%d host=%d", response.Protocol, Version)
	}
	if response.ID != request.ID {
		return fmt.Errorf("plugin response id mismatch: got=%d want=%d", response.ID, request.ID)
	}
	if response.Error != nil {
		return response.Error
	}
	if output != nil && len(response.Payload) > 0 {
		if err := json.Unmarshal(response.Payload, output); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
	}
	return nil
}

func (c *Client) processError(err error) error {
	detail := c.stderr.String()
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w; plugin stderr: %s", err, detail)
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	c.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		c.cancel()
		err = <-done
	}
	c.cancel()
	if err == nil {
		return nil
	}
	return c.processError(fmt.Errorf("plugin process exited: %w", err))
}

// boundedBuffer retains the last limit bytes written by a noisy plugin.
type boundedBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(data), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.TrimSpace(b.data))
}
