package protocol

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeHandler struct{}

func (fakeHandler) Metadata(context.Context) (Metadata, error) {
	return Metadata{Protocol: Version, Source: "example.test/fake", Version: "0.1.0", Kinds: []Kind{KindCodegen}}, nil
}

func (fakeHandler) Generate(_ context.Context, request *GenerateRequest) (*GenerateResponse, error) {
	return &GenerateResponse{Files: []File{{Path: request.Target.Name + ".txt", Data: []byte("ok")}}}, nil
}

func TestServeRoundTrip(t *testing.T) {
	hostToPluginReader, hostToPluginWriter := io.Pipe()
	pluginToHostReader, pluginToHostWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(hostToPluginReader, pluginToHostWriter, fakeHandler{})
	}()

	encoder := json.NewEncoder(hostToPluginWriter)
	decoder := json.NewDecoder(pluginToHostReader)
	payload, err := json.Marshal(&GenerateRequest{Target: &Target{Name: "python"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(wireRequest{Protocol: Version, ID: 7, Method: methodGenerate, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var response wireResponse
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 7 || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
	var generated GenerateResponse
	if err := json.Unmarshal(response.Payload, &generated); err != nil {
		t.Fatal(err)
	}
	if len(generated.Files) != 1 || generated.Files[0].Path != "python.txt" || string(generated.Files[0].Data) != "ok" {
		t.Fatalf("generated = %#v", generated)
	}

	if err := hostToPluginWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServeRejectsProtocolMismatch(t *testing.T) {
	request := wireRequest{Protocol: Version + 1, ID: 1, Method: methodMetadata}
	_, err := dispatch(context.Background(), fakeHandler{}, request)
	if err == nil {
		t.Fatal("dispatch succeeded")
	}
	remote, ok := err.(*RemoteError)
	if !ok || remote.Code != "protocol_mismatch" {
		t.Fatalf("error = %#v", err)
	}
}

func TestServeRejectsUnsupportedMethod(t *testing.T) {
	_, err := dispatch(context.Background(), fakeHandler{}, wireRequest{Protocol: Version, Method: methodRead, Payload: json.RawMessage(`{"id":"x"}`)})
	remote, ok := err.(*RemoteError)
	if !ok || remote.Code != "unsupported_method" {
		t.Fatalf("error = %#v", err)
	}
}

func TestBoundedBufferKeepsTail(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	if _, err := buffer.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "cdef" {
		t.Fatalf("String() = %q", got)
	}
}

func TestClientRunsExecutablePlugin(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := startCommand(ctx, executable, []string{"-test.run=TestProtocolHelperProcess", "--", "serve"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	if got := client.Metadata().Source; got != "example.test/fake" {
		t.Fatalf("source = %q", got)
	}
	response, err := client.Generate(ctx, &GenerateRequest{Target: &Target{Name: "python"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Files) != 1 || response.Files[0].Path != "python.txt" {
		t.Fatalf("response = %#v", response)
	}
}

func TestClientReportsPluginCrashStderr(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = startCommand(ctx, executable, []string{"-test.run=TestProtocolHelperProcess", "--", "crash"})
	if err == nil || !strings.Contains(err.Error(), "deliberate helper crash") {
		t.Fatalf("error = %v", err)
	}
}

func TestProtocolHelperProcess(t *testing.T) {
	marker := ""
	for i, argument := range os.Args {
		if argument == "--" && i+1 < len(os.Args) {
			marker = os.Args[i+1]
			break
		}
	}
	switch marker {
	case "serve":
		if err := Serve(os.Stdin, os.Stdout, fakeHandler{}); err != nil {
			t.Fatal(err)
		}
	case "crash":
		_, _ = os.Stderr.WriteString("deliberate helper crash\n")
		os.Exit(23)
	}
}
